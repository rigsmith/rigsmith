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

Job termination is asynchronous: the OS can release the worker’s lease before
every old child has exited. The persistent fence below blocks replacement writers
through that interval, without assuming job termination has already completed. The Unix
worker-death boundary is described below. Production worker commands, service
registration and queued hooks remain disabled until the remaining gates are complete.


## Unix worker-death supervision (6b.6b.2b)

An opt-in internal `process.WithSupervisor` context selects a dedicated executable
entry point that calls `process.ServeSupervisor` and immediately exits with its
result. No package initialization, environment-variable dispatch or shell wrapper
is involved. Command/hook wiring has not been enabled; ordinary synchronous
Claude operations continue to use their existing process runner. Windows retains its native job ownership and uses this selection to require
a staging lease and persistent fence; it does not launch a Unix supervisor.

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

The startup history check passes its acquired staging context into retained Git
calls. Artifact services use `WithSupervisorLease` to attach that capability only
to command supervision: capture, commit, publication, retries and manual coverage
confirmation keep the staging lease while private stores/queue persistence retain
the operation context's independent lock identity and cancellation. An expired
explicit lease is rejected; it cannot fall back to a private store's lease.

Synthetic native tests pause the supervisor before command startup and after a
writer starts, then kill the worker without defers. While cleanup is paused, a
replacement staging writer must receive `ErrBusy`. After resuming the supervisor,
all inherited output pipes must close, the store must become available, and the
old helper must not perform a later write. Both boundaries repeat with a fresh
worker. Other tests cover cancellation, normal exit, startup failure, missing
completion, request limits, command IO and ordinary exit-code classification. The existing retained-command
cleanup suite also runs through supervision, including its command/transport/stream
builders and their `WaitDelay` settings; real Git tests check refs, semantic exit
codes and binary blob IO. Claude queue integration tests cover startup, every
phase, stop/drain and offline replay after source removal under supervision.
The inherited-lease test separately checks that an expired context cannot
produce another duplicate and that closing the final duplicate releases the lock.

Canonical service workflows still use `core/gitrepo` directly. `Capture`, `Sync`,
`SyncWithCoverage`, `Publish`, `Pull`, `Reconcile`, `RepairMerge`, and `FinishMerge`
therefore reject explicitly supervised contexts before acquiring worker/staging
ownership or changing data. Their ordinary synchronous paths remain available.
The private sealed-capture path skips canonical repair and remains available to
the retained queue adapter; its owned Git phases have separate lifecycle tests. The earlier supervised coverage fixture exercised retained confirmation,
not every canonical Git command; it is now a refusal test on every platform.
Adapting those canonical calls is an explicit 6b.6b.2d gate before queued rollout.

## Persistent restart fence (6b.6b.2c)

Selecting supervision also records command intent in the existing sibling store
lock file before creating any process. The record contains a fixed version marker
and random command identity, never arguments, environment, paths, credentials, or
transcript bytes. It is written and flushed in place: replacing or deleting the
lock inode would invalidate coordination with waiting writers. This protects
against process crashes on stable local filesystems; it is not a new power-loss
or distributed-lock guarantee.

An empty lock file means no unconfirmed supervised command. Any nonempty record,
including a partial, damaged or unknown version, blocks `storelock.Acquire` with
`ErrFenced` after it gets the OS lock. Active nested acquisitions also check it;
a stale operation context cannot bypass it. While the old lease is still held,
independent contenders continue to receive the usual `ErrBusy`. Ordinary paths
never create a fence unless command supervision was explicitly selected.

On Unix, intent precedes supervisor creation. The supervisor adopts the record
through its inherited lease descriptor, runs the command, and clears the exact
record only after confirmed group cleanup. It clears before writing completion,
so a dead worker or broken result pipe does not strand an otherwise clean store.
Cancellation, nonzero command exit and failed command startup can all have
verified cleanup; command status and cleanup evidence are tracked separately.
Failure to start the supervisor itself can also clear intent because no writer
was created. Missing/malformed completion remains a protocol error; the worker
never clears intent on behalf of a supervisor that started.

The public runner rejects already-started commands before creating ownership or
intent. It does not adopt an external process or claim cleanup for one.

On Windows, intent precedes job/anchor preparation. Normal cleanup clears it only
after the existing job drain and process-handle waits, including ownership-handle
cleanup. Abrupt worker death leaves the record in place even if the job's
asynchronous termination subsequently succeeds. Losing the OS lock is not proof
that the helpers have stopped. Unsupported platforms reject supervised execution.

Darwin may reject a repeated kill while helpers are still exiting. On `EPERM`,
cleanup observes the group for at most one second and succeeds only when no
executing member remains. Expiry with live members, or an inspection failure,
retains the error and fence; the delay itself is never evidence of cleanup.

Cleanup observation failures keep the fence. A stale completion token cannot
clear a newer command's intent. A failure flushing the cleared record is returned
even if truncation already took effect. That differs from uncertain process cleanup:
`Clear` is called only after all writers are verified stopped, so an empty or
retained record is safe after such a flush failure. No timer, worker restart, ordinary retry, or
operator bypass clears an unconfirmed record. This deliberately prefers a blocked
store to overlapping writers. **Proof-based recovery and canonical Git supervision (6b.6b.2d) are still
required before production queued hooks:** this milestone does not expose an
unfence command, advise deleting lock files, or claim automatic recovery after
supervisor/Windows owner death. Queue data and retained artifacts remain intact.

Synthetic tests kill a worker after flushed intent; kill both Unix guardians while
an external writer remains alive; and check a fenced acquisition before waiting
for Windows children to exit at each creation boundary. Normal/nonzero exits,
cancellation and startup failure must release the fence after verified cleanup.
Other tests cover damaged records, expired leases, stale completion identities,
cleanup observation failure, and missing/oversized supervisor completion. Existing
worker-death tests still require an available store after a surviving Unix
supervisor successfully cleans up.

There is one additional running supervisor per retained Unix command. Trusted
helpers must stay in their inherited process group; this is not a sandbox.


## Canonical Git command-runner plumbing (6b.6b.2d.1)

`core/commandrun.WithRunner` selects a synchronous command runner on one operation
context. Core Git stays independent of vendor adapters, store locks, and process
supervision. Without a selection, command construction and execution retain their
ordinary `exec.CommandContext` / `cmd.Run` behavior. A selected runner receives a
fresh plain command and owns cancellation, output copying, cleanup, and waiting;
it must return cleanup uncertainty as an error. A nil selection fails closed.
Selection alone neither acquires a lease nor enables supervision.

Buffered commands in `core/gitrepo` and Claude backup attribute preparation use
this boundary, including finite stdin, temporary-index environment overrides,
ignore/exit probes, and binary archive output. The exit-code probe accepts only a
direct `*exec.ExitError` as an ordinary Git status under selection; a joined or
wrapped runner failure stays an error. Streaming `ShowPrefix` and terminal-attached
merge tools reject selection before creating a process: their independent
Start/Kill/Wait and interactive stdin contracts are not supported by this runner.
Ordinary previews and interactive merge tools keep their existing behavior.

This is command plumbing, not complete canonical workflow supervision. Legacy
boolean/fallback helpers can still normalize errors, and workflows also modify
files in process. All eight canonical Claude service guards remain in place.
Before removing them, the adapter must bind the active staging lease, stop the
workflow on uncertain command cleanup (including through fallback helpers), and
verify that no later in-process mutation or publication can follow that failure.
Proof-based fenced-store recovery remains a separate requirement. There is no
production runner selection or new CLI/hook behavior in this step.

Synthetic tests compare selected/default Git byte round trips and distinguish
nonzero exits from cleanup failures. Native Unix-supervisor/Windows-job tests run
attribute preparation, canonical commits, temporary-index history, and tar export
under a staging lease, verifying exact binary bytes and cleared command fences.
