# Retained-command process lifecycle

## Windows creation ownership (6b.6b.2a)

The shared `process.Run` used by retained Git operations must own each external
command and its trusted helpers before any command code runs. Cancellation and
normal exit terminate the job and wait for its active-process count to reach
zero and for the owned anchor/command handles to signal exit before returning
to the caller that owns the staging lease. Cleanup/inspection errors are joined
with command errors, including failed startup.

Previously, Windows created the command suspended, assigned it to a job, and
resumed its thread. An abrupt owner death between creation and assignment could
leave a suspended orphan outside the kill-on-close job.

Windows supports assignment during creation through `PROC_THREAD_ATTRIBUTE_JOB_LIST`.
Go 1.26's `syscall.SysProcAttr` exposes `ParentProcess`, but not the job-list
attribute. We use a suspended anchor to bridge those APIs:

1. Create a private, non-inheritable job with kill-on-last-handle-close enabled.
2. Use native `CreateProcess` with the job-list attribute to create an anchor
   from the current executable, suspended and without inherited handles.
3. Never resume the anchor. Set `exec.Cmd.SysProcAttr.ParentProcess` to its
   process handle, so the command inherits job membership during creation.
4. Keep using Go's command handling for argument quoting, environment, working
   directory, standard streams, output copying, exit status and reaping.
5. Terminate and drain the job on completion, cancellation or startup failure,
   then close the anchor, command and job handles.

The anchor inherits no job handle; neither it nor the actual command can keep
the job alive after the worker dies. Failure to create the anchor or inherit
its job is an error. There is no fallback to creation followed by assignment.
Trusted helpers must remain in the inherited job; this is not a sandbox.

This adds one suspended process per retained command. It maps the executable but
never initializes Go or runs application code. That costs process creation and
kernel resources; Windows CI timing must be watched. A future Go job-list API
would let us remove the anchor without replacing Go's command/pipe handling.
The native job-list attribute requires Windows 10 or newer, within the current
Go-supported Windows versions.

See Microsoft's [job creation explanation](https://devblogs.microsoft.com/oldnewthing/20230209-00/?p=107812)
and [process attribute reference](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-updateprocthreadattribute)
for creation-time assignment and designated-parent job inheritance.

## Validation and remaining gates

Windows-only synthetic tests kill the owning process without running defers:

- after anchor preparation, before command creation;
- after command and descendant creation, before the runner's startup observation;
- after startup observation, while command and descendant are alive.

Tests hold independent process handles before killing the owner, then require
all handles to signal exit. They repeat each case with a fresh owner and check
that inherited output pipes close. Other tests cover failed executable startup,
startup and cleanup errors being reported together,
argument/environment/directory/stdin/stdout/stderr preservation, command exit
codes, normal helper exit and cancellation. CI runs the native Windows tests;
cross-compilation alone cannot validate kernel behavior.

These tests prove child cleanup after owner death, not safe lease reacquisition.
Job termination is asynchronous, so another worker could acquire an OS-released
lease before every old child has exited. Milestone 6b.6b.2c must fence replacement
workers against that interval and validate platform restart behavior. The Unix
worker-death boundary is described below. Production worker commands, service
registration and queued hooks remain disabled until the remaining gates are complete.


## Unix worker-death supervision (6b.6b.2b)

An opt-in internal `process.WithSupervisor` context selects a dedicated executable
entry point that calls `process.ServeSupervisor` and immediately exits with its
result. No package initialization, environment-variable dispatch or shell wrapper
is involved. Command/hook wiring has not been enabled; ordinary synchronous
Claude operations continue to use their existing process runner. Windows ignores
this Unix selection and retains its native job ownership.

For each retained command on Linux/macOS:

1. Require an active staging lease and duplicate its descriptor with close-on-exec.
   Pass the duplicate explicitly to the supervisor during process creation. The
   duplicate shares the existing flock; it does not reacquire or unlock it.
2. Start the supervisor in its own session. A separate process group alone is
   insufficient: Unix job control can send SIGHUP/CONT to an orphaned stopped
   group when the worker dies. A fresh session also isolates terminal signals.
3. Pass a bounded command request, a parent-lifetime pipe and a completion pipe
   alongside the lease. Preserve the resolved command path, arguments, environment,
   directory and standard streams, including non-UTF-8 argument/environment bytes.
   No requests are written to disk.
4. The supervisor marks all protocol/lease descriptors close-on-exec before
   starting the command in its own process group through the existing runner.
   Neither Git nor its helpers inherit the lifetime pipe or staging lease.
5. Cancellation closes the worker's lifetime pipe; abrupt worker death closes it
   in the kernel. The supervisor cancels and drains the command group, then exits.
   While it remains alive, its inherited lease excludes another staging writer,
   even if the worker is gone. Normal completion also drains leftover helpers.
6. The worker requires a bounded completion record matching the supervisor's exit
   code. Ordinary command exits retain direct `*exec.ExitError` classification;
   startup/protocol/cleanup errors cannot be classified as ordinary Git exits.

The startup history check now passes its acquired staging context into retained
Git calls, so selecting supervision there does not lose the lease capability.

Synthetic native tests pause the supervisor before command startup and after a
writer starts, then kill the worker without defers. While cleanup is paused, a
replacement staging writer must receive `ErrBusy`. After resuming the supervisor,
all inherited output pipes must close, the store must become available, and the
old helper must not perform a later write. Both boundaries repeat with a fresh
worker. Other tests cover cancellation, normal exit, startup failure, missing
completion, request limits, command IO and ordinary exit-code classification.
The inherited-lease test separately checks that an expired context cannot
produce another duplicate and that closing the final duplicate releases the lock.

### Remaining restart fence

This proves cleanup after **worker** death while the supervisor survives. It does
not yet fence a supervisor killed independently, a failed cleanup inspection, or
Windows job termination still in progress after its owner's death. A failed
supervisor/cleanup must leave durable evidence that prevents a replacement worker
from treating the released OS lock as proof that all old writers stopped. That is
6b.6b.2c, required before production worker startup and queued-hook rollout.
There is one additional running supervisor per retained Unix command. Trusted
helpers must stay in their inherited process group; this is not a sandbox.
