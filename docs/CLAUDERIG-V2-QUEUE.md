# V2 durable queue core (milestone 6b.1)

`internal/agentrig/queue` persists capture intent, exclusive worker ownership and
publication progress. Both vendor adapters can consume it. It has no executable
worker, Claude service calls, queue command, hook changes or automatic activation.
Those belong to milestone 6b.2 and the later opt-in rollout. This internal
foundation has no end-user changeset because shipped commands behave as before.

## Identity and generations

Each queue is bound to credential-free identifiers for the vendor, canonical
store, source roots, remote destination and configuration revision. The adapter
must derive those identifiers from the actual resolved inputs. Changing any
binding field makes Open fail; opening an old queue with today's configuration
never redirects old jobs. Remote credentials and configuration contents do not
belong in this file; use a stable non-secret identity or digest instead.

Each request names an event, native session, source provenance and explicit
normal/selected/all flush intent. Event IDs must remain stable across retries.
Reusing an ID with different intent fails, including after acknowledgement.
Identical retries return the original generation and reflush the state before
reporting success. Generation numbers increase only for new accepted events.
Source/account provenance is an input supplied by the adapter; the queue never
looks at a current login or stamps old events with a new account.

Only never-claimed, pending batches with the same provenance coalesce. Selected
flushes union their path sets; all-flush supersedes selected/normal intent while
the individual events remain recorded. Different provenance stays separate.
The first claim seals a batch's event membership permanently. Input arriving
during capture or retry creates later work, even if the old batch has not reached
its captured phase. Acknowledgement removes that batch alone; `Through` is a
batch high-water mark, not permission to delete every lower global generation.
A delayed or blocked batch cannot be overtaken within its provenance group.

## Ownership and progress

Queue transactions take the shared OS-owned lock briefly. A Worker session holds
an independent exclusive worker lock through its lifetime, so producers can
continue enqueueing while capture runs. Drive each Worker from one sequential
executor; it does not run background code itself. Repeated Next returns the same
active claim and reflushes it, allowing a caller to recover an uncertain claim
write without creating a second capture generation.

Opening a new Worker after the previous owner closes or dies recovers running
batches as pending, preserving their membership, progress, references and retry
metadata. No timeout steals ownership from a live process. Old Worker objects
cannot write after Close, and their owner token cannot act as a replacement
worker. The queue lock and worker lock use the same canonical alias mechanics
as milestone 6a. Keep their directories stable while operations run.

Progress is explicit: queued → captured → committed → pushed → acknowledged.
Captured and committed transitions require stable artifact references. The
adapter must verify that the captured artifact is durable and immutable and that
the commit is retained; recording a string is not that verification. Pushed means
a confirmed remote result. Do not mark a local-only commit pushed merely to
finish it: local-only completion policy remains an integration decision.

Retries preserve progress, so a committed batch can resume publication without
recapturing changing live sources. Permanent failures become blocked jobs with
bounded, credential-free failure codes. New events cannot clear a block; Unblock
requires an explicit decision. Missing sources can be represented as blocked
work, never silently acknowledged as an empty capture. Completed event receipts
remain persisted so a producer retry after restart does not recreate finished
work. Repeated progress/acknowledgement writes reflush before reporting success.
Unblock also reflushes an already-cleared pending batch on retry; it cannot clear
a pending retry's backoff or failure code.

## Persistence and failure boundaries

The private queue directory contains a checksummed, versioned `queue.json` and
a separate worker lock. Create makes a new directory with mode 0700 on Unix;
snapshot files use mode 0600. Windows uses the parent directory's ACL policy.
The vendor must place this directory outside every backup/sync root.

A transaction loads and validates the complete current snapshot under the queue
lock, applies one change and writes a temporary sibling. It flushes and closes
that file before replacement. It never truncates the current state in place or
uses a remove-then-rename fallback. Corrupt checksums, unsupported schemas,
unknown fields and inconsistent generations/membership block reads and writes
without altering the file. Temporary files from an interrupted write are ignored,
not promoted over committed state. Missing state is an error, not an empty queue.
An interrupted initial Create may leave an uninitialized directory that requires
inspection/removal before retry; ordinary Open never resets it.
Create on an existing valid queue reflushes the current state before succeeding,
so retrying an uncertain initialization acknowledges durability without losing
events or worker progress added since the first attempt.

| Platform | Persistence request |
| --- | --- |
| Linux | Flush the file, rename the sibling, then fsync the queue directory and its ancestors, including newly created directory entries. |
| macOS | Flush the file with Sync and F_FULLFSYNC, rename, then fsync the directory chain. |
| Windows | FlushFileBuffers through File.Sync, then same-directory MoveFileEx with replace-existing and write-through flags. No cross-volume copy fallback. |

Go documents [File.Sync](https://pkg.go.dev/os#File.Sync) as committing file
contents to stable storage. Windows provides the write-through move option in
[MoveFileEx](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw).
These are OS durability requests, not a hardware-independent power-loss promise.
The supported layout is a stable local filesystem that honors these requests;
network/shared-host state directories are outside the contract. Process-exit
recovery is tested on native CI platforms; this is not a VM power-cut test suite.

A failure during/after replacement may mean the state committed. ErrUncertain
requires retrying the same operation with the same identity and arguments:
the same directory/binding for Create or Worker, EventID/request for Enqueue,
or batch ID and transition arguments for worker updates. Never invent a new
event ID. Idempotent write retries reflush:
merely finding the ID in the readable file is insufficient after an earlier
uncertain durability result. Read-only snapshots do not acknowledge persistence.
The queue does not promise exactly-once external side effects; capture and Git
publication must tolerate replay after a crash between an effect and its marker.

## Capacity and upgrade policy

State is bounded at 16 MiB and requests at 64 KiB. Enqueue stops at half the state
budget or 128 outstanding batches, preserving existing work. The remaining
budget is reserved for artifact references/failure codes bounded at 4 KiB each
including JSON escaping, retry metadata and
acknowledgements, allowing accepted work to drain. No event or blocked batch is
automatically discarded to make room.

This first core retains completed receipts indefinitely. Compaction with an
explicit producer replay horizon, operational status reporting and a full-queue
remedy are required before enabling high-volume hooks. Rewriting a complete
snapshot is deliberately simple and bounded; measure it with the integrated
worker before choosing a journal or database. Unknown versions fail closed.
Schema migration/rollback must run under both ownership locks and preserve
pending work and deduplication receipts; there is no automatic migration yet.

## Integration gates (milestone 6b.2)

- Recompute and validate canonical store/root/remote/configuration identity and
  source provenance before execution. Reject changed bindings rather than use a
  mutable config pointer or the worker's current login.
- Preserve a durable captured artifact across retries. A staging checkout that
  another sync can change is not itself an immutable capture reference.
- Protect requested sources from retention until captured; handle deletion and
  unavailable source attribution explicitly, without acknowledging missing data.
- Integrate manual sync acknowledgements only for the exact events its capture
  covered. A queue high-water mark alone cannot establish coverage.
- Keep lock order consistent: worker ownership before staging ownership; queue
  transactions stay short. Pass the original cancellation context to queue APIs,
  not a borrowed staging-store capability, which rejects nesting another store.
- Own Git/mergetool child processes and validate offline retry/local-commit/push
  recovery before offering a worker command or queued hooks. Queue owner recovery
  alone does not terminate orphaned external processes.
- Add the Claude command/status surface, local-only completion policy, queue
  capacity remedy and supported worker startup/draining/rollback behavior.

Validation covers duplicate IDs and conflicting retries, coalescing, provenance
separation, sealed generations, progress guards, blocked work, retry deadlines,
uncertain writes, capacity headroom, corruption/schema rejection, concurrent
processes, worker death after commit and process exit before/after publication.
The six existing Claude compatibility scenarios keep their unchanged baseline.
