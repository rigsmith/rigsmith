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

The initial restart-fence milestone refused supervised contexts at all eight
canonical service boundaries because those Git paths had not been adapted. The
canonical workflow integration below replaces that blanket gate with staging
lease validation, service-owned runner selection, and failure propagation. The
private sealed-capture path continues to skip canonical repair and retains its
separate owned Git phases.

## Persistent restart fence (6b.6b.2c)

Selecting supervision also records command intent in the existing sibling store
lock file before creating any process. The initial record contains a fixed version marker
and random command identity, never arguments, environment, paths, credentials, or
transcript bytes. The Unix recovery extension below adds bounded ownership evidence. It is written and flushed in place: replacing or deleting the
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
store to overlapping writers. **The Unix recovery extension below adds explicit proof-based recovery; Windows
recovery remains required before production queued hooks.** This milestone does not expose an
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
ignore/exit probes, and binary archive output. The exit-code probe and the semantic answers in `DeleteRef`/`MergeBase` accept
only a direct `*exec.ExitError` as an ordinary Git status under selection; a joined
or wrapped runner failure stays an error even after Git diagnostics are added. Streaming `ShowPrefix` and terminal-attached
merge tools reject selection before creating a process: their independent
Start/Kill/Wait and interactive stdin contracts are not supported by this runner.
Ordinary previews and interactive merge tools keep their existing behavior.

The plumbing milestone alone did not enable canonical workflows: boolean/fallback
helpers could still normalize failures before in-process writes. The next
milestone below binds leases and adds explicit failure checks. Proof-based
fenced-store recovery remains a separate rollout requirement.

Synthetic tests compare selected/default Git byte round trips and distinguish
nonzero exits from cleanup failures. Native Unix-supervisor/Windows-job tests run
attribute preparation, canonical commits, temporary-index history, and tar export
under a staging lease, verifying exact binary bytes and cleared command fences.


## Canonical workflow supervision (6b.6b.2d.2)

An explicitly supervised `Capture`, `Sync`, `SyncWithCoverage`, `Publish`, `Pull`,
`Reconcile`, `RepairMerge`, or `FinishMerge` now acquires/borrows staging and installs
its own `process.Run` selection. Nested services retain the same operation failure
state. An external command-runner override is refused before acquisition. An
explicit supervisor lease must match the operation's live staging capability;
expired or unrelated leases cannot be silently replaced. Private artifact/queue
contexts still keep their separate store identities. Unsupported platforms and
interactive merge-tool requests fail before side effects. Ordinary contexts keep
their synchronous behavior, and no command or hook selects supervision yet.

`commandrun` retains the first runner error other than a direct `*exec.ExitError`.
A direct exit can be a valid Git answer, such as a missing ref or a merge conflict,
with cleanup already verified by `process.Run`. Wrapped/joined failures, failed
startup, and cancellation cannot become benign answers: subsequent commands are
refused, and `commandrun.Check` exposes the failure even through boolean/fallback
helpers. Selection remains a sequential operation capability, not a concurrent
runner or a substitute for the persistent OS fence. A fresh selection must never
be used to recover an uncertain operation.

Canonical boundaries report retained failures before returning success. Capture
checks repair before writing snapshots. Conflict replacement, attribute preparation,
and directory creation check the operation before file writes. Temporary merge
files and indexes are retained when cleanup is uncertain rather than removed
under a potentially live helper. Pull skips restore and journal writes after a
runner failure; sync skips its deferred failure-journal append. `CheckIgnored`
distinguishes exit 1 from failed probes, and selected scope operations propagate
both repository-open and ignore-probe errors before changing `.gitignore`.

A nonexistent staging directory is handled before asking Git to run there; it is
an initial capture state rather than a failed command startup. Supervised manual
coverage sync holds worker ownership and staging through capture/publication, then
uses the existing retained confirmation path before acknowledging exact batches.
Failures leave queued work pending and never acknowledge a partially completed
publication. `PullResult.CommandError` exposes lifecycle failures separately from
its existing best-effort phase results.

Native synthetic tests exercise supervised capture, local Git publication,
confirmation/acknowledgement, fresh clone, and conflict-union merge completion on
Unix and Windows.
They verify released fences and matching/expired/unrelated lease handling. Injected
failures cover fallback probes, conflict-file preservation, temporary indexes,
scope ignore writes, deferred journals, and failure reporting from best-effort
history maintenance. Unix supervisor-loss tests before and
after capture verify a persistent fence and unchanged queue attempts, including
no publication or failure-journal write after supervision is lost. Existing process
ownership tests cover helper lifetime and Windows job cleanup. Pinned ordinary v1
compatibility remains a separate required check. The Unix recovery extension follows below;
there is no timer-based reset or exposed queued hook.

### Canonical review clarifications

Under selected supervision, repository initialization propagates signing and
identity configuration failures; only an unset identity (Git exit 1) permits a
fallback write. Merge repair preserves an existing directory without Git
metadata as a valid first-capture destination, but rejects failed repository
probes when Git metadata exists or cannot be inspected. Ordinary synchronous
behavior remains unchanged.

Side-branch history maintenance remains best-effort for ordinary Git failures
with verified cleanup. Runner or cleanup uncertainty always fails the workflow
and blocks later commands. A repeated subtree commit reaches the checked Git
directory probe before temporary-index removal, so an operation's retained
failure also prevents retry cleanup.

An active lease for a different store is rejected by acquisition's inode check
before canonical runner binding, Git execution, or capture. Acquisition can
create the requested store's sibling lock file and parent while resolving that
identity; this is existing lock behavior, not permission to mutate the store.
Lock files must not be removed to undo a rejected acquisition.

## Unix fenced-store recovery (6b.6b.2d.3a)

`process.RecoverStore` is an internal Linux/macOS entry point. It opens the
existing sibling lock without creating directories or a new inode, acquires
exclusive OS ownership, validates the entire fence, obtains positive process
ownership evidence, rechecks cancellation and the exact record, then clears and
flushes it in place. An existing empty lock is an idempotent no-op; a missing lock
or parent returns a filesystem error and is never recreated by recovery. A held lock, failed
proof, cancellation, damaged/unknown record, or changed command identity never
authorizes clearing. It grants no normal staging lease and performs no Git,
capture, restore, journal, queue acknowledgement, or artifact cleanup. Ordinary
acquisition still refuses every nonempty fence; recovery is never an implicit
retry or worker-startup side effect.

Recovery rejects a symlink at the lock pathname, compares the opened file with
the expected inode, and rechecks the pathname before clearing. Observed inode
replacement leaves both records intact. These checks catch accidental path
substitution; they do not defend against a malicious local actor rewriting paths
between checks or editing a fence directly.

Explicitly supervised Unix commands now write a v2 fixed-size record with a
random command token, bounded platform evidence and SHA-256 checksum. Evidence
contains the platform, hashed host/boot/process-namespace identities, and the
command's process-group ID. It contains no command arguments, environment,
source paths, credentials, or transcript bytes. The checksum detects torn phase
updates; it is not protection against a malicious local process. Existing v1
records lack recovery evidence and remain fenced, even after a reboot. They are
not guessed, upgraded, reset or deleted.

Before supervisor startup, the group is zero: no Git command has been authorized.
The inherited lease prevents recovery while the supervisor can still launch.
Inside the supervisor, `/usr/bin/true` starts a harmless group leader with an
empty environment and no inherited control descriptors. Its unreaped process
reserves the group ID. The supervisor writes and flushes that identity before
starting Git in the same group. Failure to create the anchor or seal evidence
prevents command startup. Cancellation and normal completion drain that group,
wait for Git, reap the anchor, and clear the fence only on verified cleanup.
This adds one small process and a durable evidence transition per supervised Git
command; ordinary synchronous commands retain their existing execution path.
Linux/macOS supervision now requires the standard `/usr/bin/true` utility and
readable native ownership identities; missing dependencies fail closed.

Recovery in the same boot requires the same host and process namespace. With no
authorized group it can clear once exclusive ownership is obtained. With a sealed
group, the kernel must report that the entire group is absent: `kill(-pgid, 0)`
returning `ESRCH`. The signal is zero: recovery never terminates a process. An
existing or permission-denied group remains blocked. Zombie groups and recycled
IDs can conservatively delay recovery; a missing individual PID is insufficient.
Using the kernel group lookup avoids a userspace process-enumeration race with
helpers forking during inspection.

Linux binds evidence to `/etc/machine-id`, the kernel boot UUID and the PID
namespace inode; it verifies that procfs `self` matches the caller's PID. macOS
uses `gethostuuid` and `kern.bootsessionuuid`. Identifiers are validated and hashed
before persistence. A different native boot ID on the same host proves the old
boot's processes cannot survive. A different host, changed namespace in the same
boot, or unreadable identity is refused. These are local-store guarantees: shared
network stores, cloned machine/VM identities, manually edited fences, and moving
live stores between hosts/namespaces are outside the coordination contract.

After recovery, the caller still performs the existing merge repair and secret
checks, then fresh remote confirmation before completing queued work. Tests cover
an independently killed supervisor with a surviving writer: recovery refuses
while that writer can still change files and succeeds after the fixture stops
the group. Service tests recover supervisor loss both before and after capture,
verify unchanged journal/queue state, and then complete a fresh supervised sync
and acknowledge only confirmed coverage. Other tests cover scoped evidence,
simulated boot changes, malformed records, stale completions, cancellation,
exclusive ownership and preserving retained bytes. Native CI supplies Linux and
macOS execution; simulated boot tests do not reboot the host.

Windows recovery is specified in the following extension. Neither losing the
worker lock nor failing to open a job by name proves asynchronous termination
completed. Legacy Windows records remain blocked. No production command, hook,
or automatic reset is added here.

Platform references: [Linux signal group semantics](https://man7.org/linux/man-pages/man2/kill.2.html),
[Apple syscall definitions](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/syscalls.master),
and [Windows job lifetime](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects).

## Windows fenced-store recovery (6b.6b.2d.3b)

Explicit Windows supervision now uses the shared checksummed v2 fence and
recovery transaction. It retains native job ownership at process creation and
asynchronous termination checks. Three durable phases describe what was
authorized:

- `prepared`: no Git writer has been authorized. The worker may have created a
  suspended anchor, but it never resumes that process. Recovery can clear this
  phase after obtaining exclusive store ownership.
- `owned`: the worker flushed intent before starting Git in its job. Losing a
  worker, job name, or PID never proves that every descendant stopped. In this
  phase, recovery requires a different verified kernel incarnation on the same
  host. An ordinary worker restart within the same boot remains blocked.
- `stopped`: native cleanup reported zero active job processes and waited for
  the direct process and suspended anchor to signal. The worker flushed this
  checkpoint before clearing. Recovery can finish clearing if the worker died
  between that checkpoint and `Clear`.

Failed or torn phase updates never authorize a later command. Invalid phases,
Unix group fields in Windows evidence, changed hosts, and legacy v1 fences are
refused. The protocol adds one durable transition before Git and one after
verified cleanup. Ordinary synchronous execution does not select this protocol.

Windows scope uses the validated, hashed machine GUID from the native registry
view and the creation identity of the kernel's System process (PID 4, parent 0,
session 0). `NtQuerySystemInformation(SystemProcessInformation)` supplies the
identity; reads have bounded allocation and retries, and malformed/unavailable
data fails closed. The process creation identity identifies the kernel lifetime,
including across sleep or hibernation. It is not calculated from the current
clock or uptime. A boot-entry GUID, logon session, and a boot-attempt counter are
not used as restart proof. Machine/VM clones, edited identities, and stores moved
between live Windows containers or hosts remain outside the local coordination
contract.

The conservative Windows limitation is intentional: an unconfirmed job may need
an OS restart even after its processes appear gone. Recovery never reopens a job
to terminate it, enumerates descendants to guess completion, or accepts an
operator assertion. It changes only the existing lock record; staging repair,
secret scanning and confirmed publication remain required before acknowledgement.
There is no automatic recovery at worker startup and no reset command.

Native tests kill owners at prepared, running-with-descendants, and durably
stopped boundaries, verify retained data, and ensure a drained but unconfirmed
job remains fenced in the same boot. Tests cover native scope stability,
synthetic prior-kernel evidence, malformed snapshots/phases and legacy refusal.
The restart test changes fixture evidence; CI does not reboot or hibernate the
host. Full restart and sleep/resume remain part of release lifecycle validation
before queued hooks are enabled.

References: [Microsoft process information and creation identity](https://learn.microsoft.com/en-us/windows/win32/api/winternl/nf-winternl-ntquerysysteminformation)
and [Windows job termination](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects).
