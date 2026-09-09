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
workers against that interval and validate platform restart behavior. Unix
parent-death supervision is still 6b.6b.2b. Production worker commands, service
registration and queued hooks remain disabled until those gates are complete.
