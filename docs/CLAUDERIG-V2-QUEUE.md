# V2 durable queue core (milestone 6b.1)

`internal/agentrig/queue` persists capture intent, exclusive worker ownership and
publication progress. Both vendor adapters can consume it. Milestone 6b.2 now
adds a shared one-batch phase driver and separates Claude capture from publication.
Durable capture artifacts and the capture/sealing portion of the Claude adapter
are now implemented; see [capture artifacts](CLAUDERIG-V2-CAPTURE-ARTIFACTS.md).
Retained commit bundles and Claude commit/sealing are also implemented; see
[retained commits](CLAUDERIG-V2-RETAINED-COMMITS.md). Capture-time seed retention
now keeps ancestry available before the first commit. Claude QueueAdapter now
connects these services to RunOne, including confirmed retained publication.
[Explicit foreground queue commands](CLAUDERIG-V2-QUEUE-COMMANDS.md) now provide
init, prepare/enqueue, status, retry, supervised run and drain (7b).
The v2 queue has end-user changesets for its planned release behavior, including
completion of staged merges before retrying committed batches. Shipped
synchronous commands remain unchanged. Explicit `queue sync` provides manual
coverage in 7c.2a. Hook/ordinary-sync routing and rollback remain 7c.2b work;
no background service is installed.

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
The first claim or manual-coverage preparation seals a batch's event membership permanently. Input arriving
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
the same directory/binding for Create or Worker, EventID/request and original producer timestamp for Enqueue when compaction is enabled,
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

Completed receipts remain indefinitely unless the caller explicitly establishes a
producer replay cutoff and compacts them. The shared capacity report and
compaction API are described below; status is exposed by the foreground commands.
Hook routing and command exposure of receipt/artifact cleanup remain rollout work. Rewriting a complete snapshot is deliberately
simple and bounded; measure it with the integrated worker before choosing a
journal or database. Unknown versions fail closed.

New queues still use schema 2. Ordinary operations preserve older schema-1 queues;
coverage preparation upgrades schema 1 to 2 under both ownership locks. Receipt
compaction explicitly upgrades schema 1 or 2 to 3 under the same locks, adding the
producer cutoff and retired-generation count. Coverage never downgrades schema 3.
Unfinished events, phases, seals and retry metadata survive upgrades. Older
binaries reject schema 3; there is no downgrade operation. Use a compatible build
to drain an upgraded queue.

## Queue capacity and receipt compaction (milestone 6b.7a)

`Queue.Capacity` reads validated state without changing queue bytes. It reports
serialized payload usage, the total state limit, enqueue budget/headroom, batch
limit, pending/running/blocked counts, outstanding events, completed receipts and
the durable replay cutoff. Headroom is an estimate for planning: a new request
also needs serialized event/batch overhead and, unless coalesced, a free batch slot. It does
not report filesystem free space or capture/commit artifact sizes.

The report gives explicit remedies appropriate to the state: drain accepted work,
repair and unblock failed batches, or agree a producer replay cutoff and compact
completed receipts. Draining frees batch slots but retains producer receipts;
compaction is what releases their queue bytes. No operation here deletes blocked
work, resets retries, increases limits, runs Git or starts a worker.

`Queue.CompactReceipts(ctx, before)` requires an **explicit producer contract**:
producers preserve the original enqueue timestamp on every retry, and the caller
has coordinated a cutoff beyond which those producers no longer submit old work.
There is no default age/TTL and no automatic invocation. The timestamp is supplied
by the producer, not inferred from maintenance wall time. Do not enable compaction
for a producer that assigns `time.Now()` on each retry. Reusing a retired EventID
with a fresh timestamp bypasses that contract and can recreate work; the compacted
queue no longer has the old ID with which to detect the misuse. Future hook wiring
must persist the producer timestamp and present an explicit expired-input remedy.

Under exclusive worker ownership and the queue transaction lock, compaction:

- Removes only whole completed batches whose every member's original enqueue
  time is strictly before the cutoff. One newer member keeps the whole receipt
  batch; an event exactly at the cutoff is retained.
- Keeps all unfinished events, coverage seals, artifact references, attempts,
  retry deadlines, failures and even abandoned running/pushed records unchanged.
- Persists a monotonic cutoff and retired count alongside the reduced state.
  Generation IDs never reset or get reused; validation accounts for retired gaps.
- Rejects an unknown event older than the cutoff with `ErrExpired`, including
  never-accepted late input. This is a refusal, not an acknowledgement that it was
  synchronized. Existing receipts still deduplicate first, even for older pending
  work. An old request must not be given a fresh timestamp to force acceptance.

Maintenance cannot run while a worker or manual-coverage operation owns the queue.
Concurrent producer transactions serialize before or after the compaction write.
A missing/corrupt queue is not repaired or initialized. Save failure preserves the
old snapshot or reports `ErrUncertain`; retry the same cutoff to reflush, including
when the first attempt is already visible. Reported removal counts cover only the
successful attempt. Moving the cutoff backward is refused. Process exit around
replacement leaves either the old full receipt set or the compacted set plus its
cutoff, never a compacted set without the replay guard.

This is an internal shared API. It does not compact artifact stores, remove scratch
folders, install hooks or alter ordinary synchronous Claude behavior. Synthetic
native tests cover a receipt-full drained queue accepting work again, all saved
phases, mixed-age receipt batches, schema 1/2 upgrades and subsequent coverage,
malformed state, failed/uncertain saves, process exit, canonical directory aliases,
and both transaction orderings for old producer input. The directory-alias test
skips where creating directory symlinks is unavailable. The compatibility group
runs with `CLAUDERIG_COMPAT=1` on native CI; outside that gate the legacy-reader
test skips. It requires local Git history containing the pinned revision and a Go
toolchain. The test builds the actual schema-1/2 reader from pre-compaction v2 commit
`4ccf5e5e59f9b92576e40ffa1d50a2984d6e417f`: it accepts an uncompacted schema-2
queue and rejects a real compacted schema-3 queue through both Open and Create
without changing queue bytes. Its archive/build/probe subprocesses use an explicit
runtime/cache environment, a private home, and controlled Git/Go settings; a
synthetic conflicting parent environment verifies isolation. The separate v1
command baseline remains unchanged.

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

## Integration status after 7b

- The [persisted runtime](CLAUDERIG-V2-QUEUE-RUNTIME.md) saves producer identity
  and resolves fresh inputs without reading a worker login. Explicit foreground
  producers/workers are wired; opt-in hook producers remain 7c.2b work.
- Durable capture/seed/commit dependencies, native conflict recovery and internal
  capacity/reclamation APIs are implemented. Cleanup commands remain deferred.
- Hook routing must preserve requested sources until capture and handle missing
  sources/attribution without acknowledging missing data.
- Shared coverage checkpoints and Claude evidence/confirmation services are
  implemented. Explicit `queue sync` uses the runtime bridge in 7c.2a;
  connecting ordinary sync automatically remains 7c.2b work;
  a queue high-water mark alone cannot establish coverage.
- Keep worker-before-staging lock order and short queue transactions. Pass the
  independent cancellation context, not a borrowed staging-store capability.
- Foreground workers select the audited Unix/Windows process supervision and
  startup check. Actual OS restart/hibernation validation remains a release gate;
  external merge tools remain unsupported by queued execution.
- Foreground status/retry/drain are exposed. Hook stop/drain coordination and
  rollback remain 7c.2b; local-only queued completion remains unsupported.

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
inversion. Manual sync cannot run between execution phases. The separate
[coverage service](#claude-manual-sync-evidence-milestone-6b5b) now establishes
manual-sync event coverage while owning both worker and staging. Capture and publication can finish audited staged merges or resume supported unresolved conflicts through [sealed recovery](CLAUDERIG-V2-MERGE-RECOVERY.md).

RunOne persists each successful reference, resumes only unfinished phases, and
acknowledges only its sealed batch after fresh remote confirmation. A retry after
an unmarked successful push confirms reachability without another push. Conflicts
reported by retained publication become blocked `publication-conflict` jobs;
recovery requires an explicit decision. The adapter now classifies other failures
using the bounded policy below. It adds no daemon or automatic retry loop: a
caller may now use the shared runner below to schedule `RunOne` when work is due.
Raw errors are never persisted
in queue state. Local-only completion remains unsupported.

Synthetic tests exercise real configured Git publication, offline recovery after
source deletion, later-generation preservation, cancellation between remote push
and phase persistence, absent saved artifacts, changed bindings/provenance,
conflict blocking, detached inputs, and staging ownership across phase gaps and
manual-sync attempts. Existing artifact, queue and fixed-baseline compatibility
tests remain required. Foreground startup, supervised execution and status are
now wired in 7b, with explicit `queue sync` coverage in 7c.2a;
hook/ordinary-sync routing and rollback remain 7c.2b.
Artifact/receipt cleanup command exposure and actual OS restart validation remain separate gates.

## Bounded failure policy

The shared queue RetryFailure helper calculates retry deadlines from a batch's
persisted claim count: 5 seconds, 10 seconds, 20 seconds, then a 30-second cap. Claim eight blocks instead of scheduling another attempt. Interrupted
claims count, and advancing phases does not reset the budget. Deadlines survive
reopening the queue; early calls do not consume attempts. The policy does not
sleep or install a background worker. A later batch with the same provenance
cannot overtake delayed or blocked work.

Claude applies fixed, credential-free codes, preserving the original error chain
for the immediate caller:

| Failure | Code | Action |
| --- | --- | --- |
| Git transport failure or publication not confirmed | publication-unconfirmed | Bounded retry |
| Source changed during freezing | source-changing | Bounded retry |
| Staging/artifact store busy | store-busy | Bounded retry |
| Operation deadline while caller context remains active | operation-timeout | Bounded retry |
| Scan tripwire | scan-rejected | Block |
| Config/provenance/destination mismatch | binding-mismatch | Block |
| Invalid queued work | invalid-work | Block |
| Publication/staging merge conflict | publication-conflict | Block |
| Invalid archive, retained commit or byte-conversion attributes | artifact-invalid | Block |
| Artifact size bound exceeded | capacity-exceeded | Block |
| Missing requested source or saved artifact | data-unavailable | Block |
| Filesystem permission denied | permission-denied | Block |
| Unrecognized failure | operation-failed | Block |

Git does not distinguish authentication from network failures through this
transport error type. Both receive the bounded publication budget; this does not
claim every transport error is transient. Known permanent failures take precedence
when errors are joined. Secret rejection is now typed by the engine without
changing existing diagnostic text; classification never parses Git/scanner text.

Cancellation and uncertain artifact writes stay unclassified, retaining the last
confirmed phase for recovery. Queue marker/acknowledgement persistence failures
never pass through this policy. Blocking does not discard artifacts or events,
create an acknowledgement, or infer that live data can be omitted. Repairing a
file or credentials does not automatically clear a block. Explicit Unblock grants
another attempt without resetting the claim count; an exhausted batch that fails
again blocks again. A status/recovery command and production worker scheduling
remain rollout gates.

Synthetic tests cover deadline persistence across reopen, early-call exclusion,
budget exhaustion and explicit recovery, retained-reference preservation, raw
error omission from queue state, missing-source and scan repair/unblock, joined
error precedence, cancellation and uncertain-write classification boundaries.


## Retained conflict recovery

Retained publication now resolves supported manifest and device-registry content
conflicts using Claude's existing native unions, in the private publication
repository. Proven append-only native CLI transcripts and memory text can also
keep both machines' additions through [bounded append recovery](CLAUDERIG-V2-RETAINED-PUBLICATION.md#bounded-retained-append-recovery).
Canonical version-1 chunked transcripts now use verified saved parts through
[bounded chunk recovery](CLAUDERIG-V2-RETAINED-PUBLICATION.md#bounded-retained-chunk-recovery), keeping the chunked format and on/auto defaults.
Eligible ordinary files now use [saved snapshot ordering](CLAUDERIG-V2-RETAINED-PUBLICATION.md#retained-ordinary-file-snapshots). Both ordinary-file conflict sides pass the secret tripwire before selection, including the losing snapshot. Equal/unknown times and novel merge bytes remain blocked.
The complete resolved tree is audited before pushing. Mixed native/chunked indexes, ordinary
JSONL files, edited transcript/memory history, structural conflicts and invalid metadata still return a conflict
and remain blocked. Existing blocked batches require explicit Unblock; recovery
does not clear queue state on its own. See the [retained publication contract](CLAUDERIG-V2-RETAINED-PUBLICATION.md#bounded-retained-metadata-recovery).

Already-staged canonical merges can be completed before retrying committed work; see the [completion contract](CLAUDERIG-V2-RETAINED-PUBLICATION.md#already-staged-canonical-merge-completion). The retained artifact is verified before any HEAD change. Secret rejection leaves the batch committed and blocked, and transport failure after completion reuses the same retained batch. Supported unresolved canonical conflicts now use [sealed recovery](CLAUDERIG-V2-MERGE-RECOVERY.md) before capture and publication.


## Manual-sync coverage checkpoint (milestone 6b.5a)

The shared queue now exposes `Worker.PrepareCoverage` and a session-bound
`Coverage` ticket. This is an internal integration boundary: Claude's synchronous
commands do not call it yet, and queued hooks remain disabled. The Claude service
now supplies per-request evidence and publication wiring through
[milestone 6b.5b](#claude-manual-sync-evidence-milestone-6b5b); aggregate capture
counts alone cannot establish coverage of individual requests.

The caller acquires worker ownership, validates its actual binding and source
provenance, and prepares candidates **before reading sources**, with worker
ownership held through capture, publication and acknowledgement. It acquires
staging after worker ownership and uses an independent context for queue
transactions. Preparation refuses an active execution and seals the unattempted,
pending prefix for that provenance. Previously attempted, delayed, blocked or
retained work stops the prefix, preserving its recovery path. Other provenance
is not selected. Preparation does not increment attempts, claim execution or
create an artifact reference.

Producers can still enqueue. New events get later batches, even when they name
the same source and flush intent. `Coverage.Batches` returns detached snapshots
of the candidates. The vendor must prove that each reported generation's native
sources and requested flush were included in the published result, respecting
identity, source availability and retention. A success return, a global generation
watermark or a local-only commit is insufficient. The shared queue trusts this
vendor evidence just as it trusts the retained execution adapter's phase results;
it does not inspect native files or contact a remote.

After confirmed remote publication, `Coverage.Acknowledge` accepts explicit
covered generations from that ticket. It rejects foreign generations and changed
candidate state, and records only batches whose **every** event is covered.
Partially covered batches remain pending in full; already-covered events in those
batches may be captured again. The method returns only acknowledged generations,
never a global watermark. Completed producer receipts remain available for
idempotent event retries. It neither manufactures captured/committed/pushed
references nor acknowledges saved artifacts using unrelated live input.

Abandoning a ticket leaves sealed work pending. A replacement worker can execute
it normally with its retry budget intact, or prepare a new ticket before a new
capture. The old ticket cannot write after its worker closes or loses ownership.
A failed or uncertain preparation returns no usable ticket. Retry preparation
before reading sources; a repeated preparation may include newly accepted work.
An uncertain acknowledgement returns no confirmed generations; retry the same
ticket and generation list under the same owner to reflush receipts. Process
death before a durable acknowledgement leaves work to replay, including when
publication had already happened. Process death after the receipt write preserves
completion and producer deduplication. Exactly-once publication is not promised.

Tests cover selected/all intent, noncontiguous generations and other provenance,
partial coverage, later arrivals, detached inputs, recovery barriers, stale owners,
failed/uncertain persistence, cancellation, real process death and acknowledgement
crash boundaries. Schema-1 fixtures retain completed receipts, committed artifacts
and retry metadata through the additive schema-2 upgrade.


## Claude manual-sync evidence (milestone 6b.5b)

`Service.SyncWithCoverage` composes synchronous capture/publication with an
explicit, existing queue. It acquires worker ownership before staging, verifies
that queue storage is outside all resolved source and staging roots, and checks
the current configuration binding. Live identity is read once at the ordinary
capture point, before resolving manual flush intent. Identity errors or invalid
provenance never become unknown-account acknowledgements. Queue transactions use
an independent operation context; both leases remain held until confirmation and
acknowledgement finish. Producers may enqueue later generations throughout.

Before capture, the service seals candidates and resolves each requested CLI
session to one native transcript. Coverage includes its subagents, any selected
flush groups, and all allowlisted CLI transcripts for an all-flush request.
Missing or ambiguous sessions and unavailable selected paths remain pending.
Requested sources are freshly read even when size and modification time match
staging; changed-file throttling, retention, size limits and secret policy still
apply. Evidence requires a regular source with stable file identity, size and
modification time across the read. Native source paths remain in memory and are
excluded from serialized reports and journals.

After capture and pruning, a second group walk detects newly appearing members.
Every required member must have fresh capture evidence and a readable retained
snapshot. The session ledger must exist and match the account when provenance
has a known account UUID. A successful identity read with no UUID supports
explicit unknown provenance; failed or invalid reads cannot acknowledge it. The service
hashes logical transcript bytes, validating chunk indexes and parts, and keeps
only completely covered batches. Deferred, skipped, missing, pruned, oversized
or partially covered groups remain pending with their retry budget unchanged.
Chunking and redaction are verified in their resulting backup representation.

Coverage publication refreshes tracked Git contents even when the index stat
cache matches; ordinary sync retains its existing incremental staging.
Publication records the original snapshot commit before reconciliation or
history maintenance. `commitartifact.ConfirmSnapshot` freshly fetches the bound
remote using existing system Git/`gh` configuration into a temporary private
repository. It requires the snapshot in the fetched history, validates and audits
its raw committed tree, and compares its logical transcript hashes and account
ledger with capture evidence. It does not trust cached remote refs or reread live
sources after publication. A concurrent remote append or successful reconciliation
can preserve this proof. Rewritten or squashed-away snapshot history cannot, even
when an earlier push succeeded; the queue remains pending for a later capture.
Temporary confirmation data is removed when the operation returns.

Only confirmed complete batches reach `Coverage.Acknowledge`. Local-only and dry
runs do not prepare or acknowledge candidates. Capture, scan, publication,
confirmation or cancellation failures cannot acknowledge work. The result retains
ordinary sync progress when a later confirmation or acknowledgement fails.
External merge tools are rejected at this boundary until their process lifetime
can be covered by worker lifecycle controls. Configured transport validation runs
before capture; the existing HTTPS/Git authentication path and absolute local
fixture paths are reused without adding credentials or transport mechanisms.

The 7c.1 [runtime bridge](CLAUDERIG-V2-QUEUE-RUNTIME.md#manual-sync-coverage-bridge-7c1)
validates the persisted lifecycle before using this manual-sync coverage service.
The explicit `queue sync` command uses it in 7c.2a; automatic hook routing remains
pending.
Ordinary `Sync` keeps its existing behavior. Explicit foreground queue commands
are wired in 7b, with explicit manual coverage in 7c.2a; ordinary-sync/hook
routing and rollback remain 7c.2b. Cleanup command
exposure and actual OS restart validation remain separate gates;
Desktop request routing and the separate Codex adapter remain future work.

Synthetic tests cover later arrivals, worker/staging exclusion, identity/flush
ordering, same-metadata source changes, selected subagents and all-flush batches,
retention/size/scan failures, native and chunked/redacted snapshots, late group
members, local/dry runs, cancellation, remote rewrites and push reconciliation.
Shared confirmation tests cover SHA-1/SHA-256 repositories, exact raw snapshot
bytes, false fetched refs, policy rejection, size bounds and scratch cleanup.


## Worker loop and controlled shutdown (milestone 6b.6a)

`Queue.Run` schedules the existing adapter and `RunOne` driver. It is an internal,
in-process boundary; it does not install a daemon, service, producer or hook.
A separate runner lease excludes duplicate loops for the same queue. Each batch
acquires its existing worker/staging leases and releases them after execution
cleanup. Idle and backoff waits hold neither lease, allowing manual sync/coverage
and foreground recovery. A competing foreground owner makes the loop wait;
explicit drain mode reports that contention instead of replacing the owner.
All queue operations use the independent operation context.

The loop reads persisted state before attempting execution, so idle polls do not
claim batches, increment attempts or rewrite queue ownership. It observes the
same first-batch-per-provenance ordering as `Worker.Next`. Blocked or delayed
work cannot be overtaken within that provenance; independent provenance can
continue. A one-second default poll detects independent-process enqueues. An
optional wake channel reduces same-process latency but is only a hint; closing it
disables the hint instead of causing a busy loop. The next eligible persisted
retry deadline can wake the loop sooner than the configured poll interval.

`ExecutionResult.FailureRecorded` is true only when an adapter's classified
failure has been durably scheduled or blocked. The loop can continue after that
result. Unknown errors, cancellation and failed/uncertain queue writes stop it;
a wrapped classification cannot hide an uncertain retry-marker write. Retry
deadlines already due when the attempt started stop the loop instead of spinning.
The adapter retains responsibility for backoff and retry-budget policy. Raw
errors are never written to queue state; an optional observer receives outcomes
after execution cleanup and worker release and must return promptly.

Signaling `Stop` requests graceful shutdown: the current batch can finish remote
confirmation, durable acknowledgement and cleanup, then the loop stops before
claiming more work. Context cancellation instead reaches the current adapter and
waits for its cleanup contract before returning. Graceful success does not mean
that every accepted request has completed. `RunResult.CompletedBatches` counts
only individually acknowledged batches, with no generation-watermark shortcut.

`Drain` processes ready batches until none are eligible. An empty observation
returns success; blocked or delayed work returns `DrainPending`/`ErrUndrained`
with remaining/blocked counts and the next eligible retry time when available.
It does not wait through backoff, discard blocked work or silently call it drained.
Producers must be stopped externally before draining the entire backlog. New
input can extend a live drain or arrive after its final empty observation; this
API does not provide an admission fence. Stop and cancellation remain available
to bound the operation. A restart reopens durable phases and resumes only their
unfinished effects, preserving later arrivals and producer receipts.

Tests cover polling/wake hints, idle ownership and no state rewrites, duplicate
runners, graceful and immediate shutdown, retry deadlines, blocked-provenance
ordering, incomplete drains, failed/uncertain markers, process death after commit
and saved-phase restart. A synthetic Claude adapter round trip stops after push
but before confirmation, finishes that batch, preserves a later arrival and then
drains it through a new run. Successive captures from the same canonical seed
also preserve both machine-journal appends. This adds a narrow retained policy
for `journal/<machine>.jsonl`: validate each record's time, operation and outcome, match its machine through
the journal writer's filename sanitization, preserve the existing base byte-for-byte,
and append both tails. Unknown fields (including UUID-like names) stay opaque;
transcript ID/index rules do not apply. Duplicate top-level fields remain invalid.
Independently created journal files can use an empty base. Edited/truncated
history, malformed records and other JSONL locations remain conflicts; the full
publication tree still passes the existing secret audit. Journal rotation that
removes the common prefix remains blocked for explicit recovery.

The Claude integration fixture initializes a shared Git history before worker
startup. Unrelated root histories remain unsupported. The optional startup check below
verifies initial common ancestry; initialization remains an explicit foreground
operation before enabling queued execution.

A crashed process releasing its OS leases alone cannot prove orphaned helpers
have stopped. The later 6b.6b.2 process-supervision and fencing work supplies that
ownership boundary, selected by the 7b foreground commands. Actual OS restart/
hibernation validation remains a release gate. Hook producer stop/drain
coordination and rollback remain 7c.2b; synchronous commands and hooks keep their
existing behavior.


## Startup history check (milestone 6b.6b.1)

`RunOptions.CheckStartup` is an optional composition point for a supervisor. It
runs once per `Queue.Run`, including an empty drain, after acquiring the runner
lease and before any claim. It receives the queue binding and independent
operation context. An error stops startup without changing attempts, retry
schedules, saved phases or receipts. Restart runs the check again. A stop already
observed before startup skips the check; a stop during the check waits for it to
finish and prevents the next claim. Context cancellation reaches the check,
which must finish its child cleanup before returning. Startup errors are returned
directly with context, not converted into durable batch failure codes.

Claude's `Service.CheckQueueStartup` accepts freshly resolved `QueueInputs` and
the runner-supplied binding. It verifies the destination/branch and disjoint
private stores, owns staging, then recomputes the capture binding and reads a
settled canonical HEAD. It does not read the worker's account or native transcript
files. Missing/unborn history, an unfinished merge or a binding mismatch
stops before contacting the destination. The check creates a temporary directory under the commit store for its private
repository and removes that temporary directory on success and failure.

The shared `commitartifact.CheckStartupHistory` imports the exact canonical
commit and freshly fetches the destination through the existing Git/`gh`
transport. Both histories must be complete and have a common ancestor. Either
side may be ahead, and divergence from a common ancestor is allowed. An absent
remote branch or unrelated roots returns `ErrSharedHistory`; network failures,
false returned refs and shallow histories remain errors. It never trusts cached
remote-tracking refs, creates an initial commit, pushes, or changes canonical
refs, index or working files. Initialize or repair history explicitly through
foreground workflows, then retry startup.

This is an observation, not a publication receipt or content audit. A remote
rewrite or canonical change after startup can still invalidate a later batch;
existing capture/publication binding, history, conflict and secret checks remain
in force. The callback is optional for generic callers and low-level `RunOne`
recovery is unchanged. Production supervision must supply the Claude check with
fresh inputs. The 7b foreground worker calls it before claims, with explicit
process supervision. Queued hooks remain disabled and actual OS restart/
hibernation validation remains a release gate.

Synthetic tests cover SHA-1/SHA-256 ancestry, diverged/ahead tips, fresh remote
rewrites, missing/shallow/false histories, offline/canceled fetches, unchanged
queue attempts and staging bytes, lease/scratch cleanup, and a checked Claude
worker stop/drain/restart round trip.

## Windows child startup ownership (milestone 6b.6b.2a)

Retained Git commands on Windows now inherit their kill-on-close job at creation,
including before the Go runner observes startup. A suspended anchor is itself
created atomically inside the job and never executes application code. The
[process lifecycle contract](CLAUDERIG-V2-PROCESS-LIFECYCLE.md) describes the
mechanism, its extra-process cost, and synthetic crash tests.

This closes the Windows suspended-child assignment gap. It does not establish
that cleanup has finished before another worker acquires a crash-released lease:
Windows job termination is asynchronous. Unix parent-death supervision and
cross-platform restart fencing are supplied by the later 6b.6b.2b/6b.6b.2c
work. The 7b foreground commands select those controls; queued hooks remain disabled.


## Explicit maintenance ownership

`Queue.Maintain` is the internal ownership boundary for queue-aware artifact
reclamation. It holds worker and transaction leases and durably reflushes validated
state before invoking a sequential callback. It changes no logical queue state and
a failed/uncertain reflush cannot authorize deletion. The callback-scoped proof
reports unfinished work and accepted-generation history; it expires on return.
Producers, workers and manual coverage stay excluded until cleanup finishes.
Callbacks must not reenter queue operations. See [queue-aware artifact
reclamation](CLAUDERIG-V2-CAPTURE-ARTIFACTS.md#queue-aware-archive-reclamation-6b7b2b)
for the idle-only sealed-archive policy and queue-parent confirmation cleanup.

## Persisted Claude runtime

The internal [Claude queue runtime](CLAUDERIG-V2-QUEUE-RUNTIME.md) binds one local
lifecycle to fixed private stores and durably saves producer identity before
accepting events. Its adapter validates the lifecycle binding before entering the
existing capture-policy layer. The [7b foreground commands](CLAUDERIG-V2-QUEUE-COMMANDS.md)
use this runtime; ordinary sync and hooks remain synchronous.
