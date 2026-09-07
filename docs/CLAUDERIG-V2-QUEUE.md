# V2 durable queue core (milestone 6b.1)

`internal/agentrig/queue` persists capture intent, exclusive worker ownership and
publication progress. Both vendor adapters can consume it. Milestone 6b.2 now
adds a shared one-batch phase driver and separates Claude capture from publication.
Durable capture artifacts and the capture/sealing portion of the Claude adapter
are now implemented; see [capture artifacts](CLAUDERIG-V2-CAPTURE-ARTIFACTS.md).
Retained commit bundles and Claude commit/sealing are also implemented; see
[retained commits](CLAUDERIG-V2-RETAINED-COMMITS.md). Capture-time seed retention
now keeps ancestry available before the first commit. Claude QueueAdapter now
connects these services to RunOne, including confirmed retained publication. A
worker command and hook activation are still pending.
This internal foundation has no end-user changeset because shipped commands
behave as before.

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

## Execution driver and Claude service boundary (milestone 6b.2, first slice)

`Queue.RunOne` acquires worker ownership, claims one eligible batch, and runs only
its unfinished phases through a required vendor `Adapter`. It returns ErrEmpty
when nothing is eligible; it does not wait, run a background loop, or activate
hooks. Two callers cannot execute the same queue concurrently. Producers can
still enqueue while a batch is executing, and only that sealed batch is
acknowledged on completion.

`Adapter.Begin` receives the queue binding and detached work snapshot. It must
validate actual configuration, provenance and saved references, then acquire the
staging lease. Its `Execution` seals a durable capture, creates/retains a commit,
and confirms remote publication. The driver persists each successful phase
before calling the next method. Committed retries skip capture and commit;
already-pushed recovery only acknowledges the batch. Failure or uncertainty in a
phase marker stops the driver immediately. External effects still require
idempotency by batch ID: if the effect happened but its marker did not, the
adapter must find and reuse it when called again.

Typed `ExecutionFailure` values choose a bounded failure code, retry deadline and
whether work blocks. Only that classification is persisted; raw error text is
not stored. An adapter's operation-level timeout can carry that classification
while the RunOne context is still active. Unclassified errors and cancellation
of the RunOne context stop with the last confirmed phase retained, without
recording a new retry classification. A retry-classification persistence failure
remains visible to the caller. Execution cleanup runs before worker ownership releases;
adapters own child-process cleanup and staging lease release. Queue transactions
always use the original context, not the staging lease's derived context.

Claude's `Service.Capture` now exposes its existing repair/capture/scan/metadata
phase. `Service.Sync` still composes Capture and Publish under one staging lease,
with unchanged journal timing, identity observation, dry runs and terminal events.
Capture alone does not commit the new snapshot or publish it (repair may finish
an earlier merge). It returns a report over a **mutable** staging tree, which
is not itself a durable capture artifact. `Service.CaptureArtifact` now provides
a separate frozen-input and sealed-output path with explicit provenance; see
the [capture artifact contract](CLAUDERIG-V2-CAPTURE-ARTIFACTS.md). Standalone
Capture keeps its synchronous live-identity and retention behavior.

Tests cover phase resumption, offline retry, new input during capture, staging
and worker ownership, cancellation, uncertain/failed markers and classified
blocking. A synthetic Claude capture/publication test changes and deletes live
sources between phases and confirms the original local commit reaches a local
bare remote without another identity observation. The concrete Claude adapter
now has additional end-to-end queue tests described below; production lifecycle
and rollout gates remain open. The unchanged six-scenario compatibility baseline
remains the gate for synchronous behavior.

## Remaining integration gates (milestone 6b.2)

- Add production producers and a resolver that persist/retrieve source provenance
  and freshly resolve configuration for QueueAdapter. The adapter now validates
  binding and provenance without consulting the worker's login.
- Durable captures, capture-time seed bundles and retained commits now preserve
  their dependencies independently of staging and now feed Push/recovery through
  QueueAdapter. Define safe artifact cleanup and classified retry policy.
- Protect requested sources from retention until captured; handle deletion and
  unavailable source attribution explicitly, without acknowledging missing data.
- Integrate manual sync acknowledgements only for the exact events its capture
  covered. A queue high-water mark alone cannot establish coverage.
- Keep lock order consistent: worker ownership before staging ownership; queue
  transactions stay short. Pass the original cancellation context to queue APIs,
  not a borrowed staging-store capability, which rejects nesting another store.
- Retained Git commands now own cancellation cleanup. Establish parent-death
  recovery and ownership of future external merge tools before offering a worker
  command or queued hooks. Offline and unmarked-push replay now have integrated
  queue tests. Queue owner recovery alone does not terminate orphaned external processes.
- Add the Claude command/status surface, local-only completion policy, queue
  capacity remedy and supported worker startup/draining/rollback behavior.

Validation covers duplicate IDs and conflicting retries, coalescing, provenance
separation, sealed generations, progress guards, blocked work, retry deadlines,
uncertain writes, capacity headroom, corruption/schema rejection, concurrent
processes, worker death after commit and process exit before/after publication.
The six existing Claude compatibility scenarios keep their unchanged baseline.

### Retained publication boundary

The [shared retained publication engine](CLAUDERIG-V2-RETAINED-PUBLICATION.md)
consumes committed bundles, privately merges newer histories and confirms remote
ancestry after publication attempts. It does not write queue markers or enable a
worker. Claude's `Service.PublishArtifact` now validates committed-batch bindings,
explicit transport destination/branch, settled local HEAD, raw native attributes
and secrets. The [local Git adapter](CLAUDERIG-V2-GIT-TRANSPORT.md) and owned
cancellation cleanup remain after the custom network transports are removed.
`NewConfiguredGitTransport` now reuses existing Git/`gh` authentication for
network publication. Claude QueueAdapter now supplies execution wiring.
Native conflict recovery, parent-death recovery and lifecycle/capacity remedies
remain gates; broad credential discovery is deferred.

## Claude queue execution adapter

`service.QueueAdapter` implements `queue.Adapter`. Its required resolver receives
only the queue binding and batch provenance ID, and returns freshly resolved
configuration, saved source identity, explicit Desktop profiles, separate capture
and commit stores, and a destination-bound transport. It must use saved producer
attribution, never the worker's active login. Existing repository privacy checks
remain the composition caller's responsibility. The queue directory must also
remain outside native roots, staging and artifact stores.

Begin detaches and validates configuration and sealed events, rejects changed
bindings or a mismatched/missing remote, then owns staging until Close. Saved
capture/commit references must match their request keys and pass archive checks;
full retained Git history/descriptor validation occurs before publication. A
committed batch depends on its retained commit bundle, not on live transcripts
or a still-present capture archive. Missing saved artifacts are errors and are
never silently rebuilt. Changed inputs cannot redirect an already-open execution.

All Claude artifact services now acquire staging **before** private artifact
writer locks. The execution keeps a staging context solely for borrowing that
lease; archive builders, seed/commit stores, private capture trees and queue
transactions receive independent operation contexts. This avoids borrowing one
store's capability to access another and prevents a capture-store/staging lock
inversion. Manual sync cannot run between execution phases; this exclusion does
not yet establish manual-sync event coverage or advance canonical staging.

RunOne persists each successful reference, resumes only unfinished phases, and
acknowledges only its sealed batch after fresh remote confirmation. A retry after
an unmarked successful push confirms reachability without another push. Conflicts
reported by retained publication become blocked `publication-conflict` jobs;
recovery requires an explicit decision. Other errors, including offline transport,
missing sources/artifacts, scan rejection and binding mismatch, retain their
original errors and last durable phase. The caller must choose retry and
remediation policy; this adapter adds no daemon or automatic retry loop. Raw
errors are never persisted in queue state. Local-only completion remains unsupported.

Synthetic tests exercise real configured Git publication, offline recovery after
source deletion, later-generation preservation, cancellation between remote push
and phase persistence, absent saved artifacts, changed bindings/provenance,
conflict blocking, detached inputs, and staging ownership across phase gaps and
manual-sync attempts. Existing artifact, queue and fixed-baseline compatibility
tests remain required. Worker startup, parent-death recovery, capacity remedies,
artifact/receipt cleanup, explicit status and opt-in hook rollout remain future work.
