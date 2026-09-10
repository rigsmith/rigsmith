# ClaudeRig and CodexRig v2 roadmap

Keep `clauderig` and `codexrig` as separate tools, with independent command trees, configuration, state directories, backup repositories, installation entries, and vendor integrations. Share Go packages inside rigsmith. No umbrella executable or mandatory shared daemon is needed.

Preserve existing ClaudeRig installations while introducing shared mechanics and
separate vendor adapters. The queue remains the first shared product improvement;
it builds on the tested synchronous workflow.

## Progress

**Completed:** store coordination (6a) merged into `codex/v2` through
[#308](https://github.com/rigsmith/rigsmith/pull/308) as `f2ad7302`.
**Completed:** durable queue storage and recovery (6b.1) in
[#309](https://github.com/rigsmith/rigsmith/pull/309) as `2853c4fb`, including review fixes.
**Completed:** the shared execution driver and Claude capture/publication split
merged in [#310](https://github.com/rigsmith/rigsmith/pull/310) as `3bd0d8fe`.
**Completed:** durable artifacts and the Claude capture/sealing adapter merged in
[#311](https://github.com/rigsmith/rigsmith/pull/311) as `3d12d3d1`.
**Completed:** retained Git bundles and the Claude commit adapter merged in
[#315](https://github.com/rigsmith/rigsmith/pull/315) as `6ffffa92`.
**Completed:** capture-time seed retention in
[#317](https://github.com/rigsmith/rigsmith/pull/317) as `4ebcd852`
([contract](CLAUDERIG-V2-RETAINED-COMMITS.md)).
**Completed:** shared retained publication merged in [#318](https://github.com/rigsmith/rigsmith/pull/318)
as `151a4d47`, with private merges and fresh remote confirmation.
**Completed:** Claude retained publication policy merged in [#319](https://github.com/rigsmith/rigsmith/pull/319) as `674830b1`: committed-batch bindings,
explicit transport destination checks, settled local HEAD, raw-tree native
attributes and secret auditing ([contract](CLAUDERIG-V2-RETAINED-PUBLICATION.md)).
**Completed:** owned subprocess cancellation/cleanup introduced in
[#323](https://github.com/rigsmith/rigsmith/pull/323) and extended to all retained
Git commands and streams in [#324](https://github.com/rigsmith/rigsmith/pull/324)
as `1006a009`, including output rejection and follow-up HEAD probe fixes.
**Completed:** [#326](https://github.com/rigsmith/rigsmith/pull/326) removed the
custom HTTPS/SSH transports and proposed generic credential helper as `b056da83`.
The local fixture adapter, process cleanup and retained publisher remain.
**Merged in [#327](https://github.com/rigsmith/rigsmith/pull/327):** retained publication using existing system Git configuration and
`gh auth setup-git`, tested with real Git/gh and a synthetic stored login.
**Merged in [#332](https://github.com/rigsmith/rigsmith/pull/332):** Claude queue execution now connects the sealed capture, retained
commit and confirmed publication services under one staging lease. Offline and
unmarked-push recovery preserve saved phases and later generations. Conflicts
block for deliberate recovery.
**Merged in [#336](https://github.com/rigsmith/rigsmith/pull/336):** bounded retry/backoff for temporary failures and explicit blocking
for scan, data, binding and unknown failures.
**Merged in [#337](https://github.com/rigsmith/rigsmith/pull/337):** private retained conflict resolution for manifest/device metadata,
reusing native unions and auditing the resolved tree before publication. Native transcript/memory append recovery merged in #341. Canonical chunk-index
recovery merged in [#342](https://github.com/rigsmith/rigsmith/pull/342); ordinary-file snapshot ordering merged in [#343](https://github.com/rigsmith/rigsmith/pull/343). Already-staged canonical merge completion merged in [#344](https://github.com/rigsmith/rigsmith/pull/344); staged completion before fresh capture merged in [#346](https://github.com/rigsmith/rigsmith/pull/346). Private unresolved-merge planning merged in [#347](https://github.com/rigsmith/rigsmith/pull/347). Recoverable file/index application merged in [#348](https://github.com/rigsmith/rigsmith/pull/348); queue recovery integration merged in [#357](https://github.com/rigsmith/rigsmith/pull/357). Queued hooks and CodexRig
remain planned. Broad
credential providers and SSH-agent/keychain discovery are deferred.

| Milestone | Status | Evidence / remaining work |
| --- | --- | --- |
| Preparation: secret scanning, transcript chunking, Git byte preservation | Complete | Included in the v1 foundation before alignment. Later preparatory fixes from #295 were carried into main by #299. |
| 1. Compatibility fixtures | Merged in v1 and v2 | [#289](https://github.com/rigsmith/rigsmith/pull/289), then included in main by [#299](https://github.com/rigsmith/rigsmith/pull/299). Fixed baseline and synthetic tests run on Linux, macOS, and Windows. |
| 2. Synchronous application services | Merged in v1 and v2 | [#299](https://github.com/rigsmith/rigsmith/pull/299). Includes capture, identity, journaling, publication, merge repair, pull, and explicit flush intent. |
| 3. Claude root/file policy adapter | Merged in v2 | [#304](https://github.com/rigsmith/rigsmith/pull/304). Root selection, file classification, retention, transform and merge selection, and session/subagent grouping. Merged as `13795f5`; implementation CI passed on all three platforms. |
| 4. Shared file processing and restore | Merged in v2 | [#306](https://github.com/rigsmith/rigsmith/pull/306), merged as `8f5a13d7`. Shared walking, snapshots, reconciliation and guarded restore; includes the dot-prefixed directory-link review correction. |
| 5. Shared session/metadata and Git publication boundaries | Merged in v2 | [#307](https://github.com/rigsmith/rigsmith/pull/307). Shared recording/query and audited publication workflows; Claude retains native formats and policies. Local synthetic suite, six baseline compatibility scenarios and vet passed. |
| 6a. Store coordination | Merged in v2: [#308](https://github.com/rigsmith/rigsmith/pull/308) | OS-owned locks across staging workflows. Linux, macOS and Windows CI passed. [Contract](CLAUDERIG-V2-COORDINATION.md). |
| 6b.1. Durable queue storage and recovery | Merged in v2: [#309](https://github.com/rigsmith/rigsmith/pull/309) | Persist events, coalesce pending flushes, preserve new generations during capture, track phases/retries and recover exclusive worker ownership. [Contract](CLAUDERIG-V2-QUEUE.md). |
| 6b.2a. Phase driver and frozen capture | Merged: #310/#311 | [Capture contract](CLAUDERIG-V2-CAPTURE-ARTIFACTS.md). |
| 6b.2b. Retained commit bundles and capture seed | Merged: #315/#317 | [Commit contract](CLAUDERIG-V2-RETAINED-COMMITS.md). |
| 6b.2c. Shared publisher and Claude policy | Merged: #318/#319 | [Publication contract](CLAUDERIG-V2-RETAINED-PUBLICATION.md). |
| 6b.2d. Owned Git processes and existing Git/`gh` integration | Merged through #327 | #323/#324 cleanup; #326 removed custom transports. [Transport contract](CLAUDERIG-V2-GIT-TRANSPORT.md). |
| 6b.2e. Claude queue execution and saved-phase recovery | Merged: #332 | One staging lease and exact queued-batch acknowledgement. |
| 6b.2f. Retry/backoff and failure blocking | Merged: #336 | Bounded retries; explicit unblock after repair. |
| 6b.2g. Retained manifest/device conflict recovery | Merged: #337 | Removal-aware metadata unions and merge-driver isolation. |
| 6b.2h. Native JSONL/memory append recovery | Merged: #341 | Preserve both tails; block changed history and ambiguous UUIDs. |
| 6b.2i. Chunked-transcript append recovery | Merged: [#342](https://github.com/rigsmith/rigsmith/pull/342) | Verify immutable side parts, reuse native record policy, preserve chunked output. 32 MiB worst-case union limit. |
| 6b.3. Ordinary file conflict policy | Merged: [#343](https://github.com/rigsmith/rigsmith/pull/343) | Select the newer recorded source snapshot; synthetic merge timestamps do not refresh copied bytes. Equal/unknown origins remain blocked. Both conflict sides are scanned; malformed profile roots are excluded. |
| 6b.4a. Finish staged canonical merges | Merged: [#344](https://github.com/rigsmith/rigsmith/pull/344) | Audit the exact staged tree and both parent tips, commit without staging files, and resume interrupted cleanup before retained publication. |
| 6b.4b.1. Staged completion before fresh capture | Merged: [#346](https://github.com/rigsmith/rigsmith/pull/346) | Audit and finish an already-staged merge before seed retention; preserve completed ancestry across later capture failures. |
| 6b.4b.2a. Private unresolved-merge plan | Merged: [#347](https://github.com/rigsmith/rigsmith/pull/347) | Recreate supported content conflicts, require matching index entries, and export an audited candidate bundle without changing canonical state. [Contract](CLAUDERIG-V2-MERGE-PLANS.md). |
| 6b.4b.2b. Safe plan application and restart | Merged: [#348](https://github.com/rigsmith/rigsmith/pull/348) | Seal a repair intent, check AUTO_MERGE/live files, durably stage files/index, and resume interrupted writes. [Contract](CLAUDERIG-V2-MERGE-STAGING.md). |
| 6b.4b.2c. Unresolved recovery in queue services | Merged: [#357](https://github.com/rigsmith/rigsmith/pull/357) | Replay the sealed candidate across staging, HEAD update and cleanup before fresh capture/publication. [Contract](CLAUDERIG-V2-MERGE-RECOVERY.md). |
| Windows CI follow-up | Merged: [#358](https://github.com/rigsmith/rigsmith/pull/358) | Run existing Windows groups concurrently; preserve all tests, per-group limits and the required aggregate check. |
| 6b.5a. Shared manual-sync coverage checkpoint | Merged: [#359](https://github.com/rigsmith/rigsmith/pull/359) | Seal exact candidate batches before capture; acknowledge complete covered batches while preserving later arrivals, partial coverage and recovery work. [Contract](CLAUDERIG-V2-QUEUE.md#manual-sync-coverage-checkpoint-milestone-6b5a). |
| 6b.5b. Claude manual-sync coverage evidence | Merged: [#363](https://github.com/rigsmith/rigsmith/pull/363) | `SyncWithCoverage` requires fresh session/subagent bytes, matching provenance, retained files and fresh remote confirmation before acknowledging complete batches. Command/hook wiring stays in rollout. [Contract](CLAUDERIG-V2-QUEUE.md#claude-manual-sync-evidence-milestone-6b5b). |
| 6b.6a. Worker loop and controlled shutdown | Merged: [#365](https://github.com/rigsmith/rigsmith/pull/365) | Exclusive runner, idle/backoff scheduling, graceful stop, explicit incomplete drains and saved-phase restart. [Contract](CLAUDERIG-V2-QUEUE.md#worker-loop-and-controlled-shutdown-milestone-6b6a). |
| 6b.6b.1. Startup shared-history validation | Merged: [#367](https://github.com/rigsmith/rigsmith/pull/367) | Optional runner preflight and Claude binding/staging checks; fresh complete ancestry without claiming work or initializing/pushing history. [Contract](CLAUDERIG-V2-QUEUE.md#startup-history-check-milestone-6b6b1). |
| 6b.6b.2a. Windows child ownership at creation | Merged: [#368](https://github.com/rigsmith/rigsmith/pull/368) | Atomically place a suspended anchor in the job and inherit ownership when starting commands. Forced owner-death tests cover startup boundaries. [Contract](CLAUDERIG-V2-PROCESS-LIFECYCLE.md). |
| 6b.6b.2b. Unix parent-death supervision | Merged: [#369](https://github.com/rigsmith/rigsmith/pull/369) | Explicit per-command supervisor in a separate session, inherited staging lease, queued phase/retry integration and worker-death tests before/after command startup. [Contract](CLAUDERIG-V2-PROCESS-LIFECYCLE.md#unix-worker-death-supervision-6b6b2b). |
| 6b.6b.2c. OS restart fencing and validation | Merged: [#370](https://github.com/rigsmith/rigsmith/pull/370) | Persistent intent before process creation; verified cleanup clears it, while owner/supervisor death or uncertain cleanup blocks replacement writers. [Contract](CLAUDERIG-V2-PROCESS-LIFECYCLE.md#persistent-restart-fence-6b6b2c). |
| 6b.6b.2d.1. Canonical Git command-runner plumbing | Merged: [#371](https://github.com/rigsmith/rigsmith/pull/371) | Vendor-independent runner selection for buffered core Git and backup attributes; explicit streaming/interactive refusal; real supervised Git byte-preservation tests. [Contract](CLAUDERIG-V2-PROCESS-LIFECYCLE.md#canonical-git-command-runner-plumbing-6b6b2d1). Workflow adaptation follows below. |
| 6b.6b.2d.2. Canonical workflow supervision | Merged: [#372](https://github.com/rigsmith/rigsmith/pull/372) | Active staging-lease binding, persistent operation failure checks, protected file/attribute/journal writes, and error-bearing ignore probes. Native supervised coverage/clone/reconciliation and supervisor-loss tests. Interactive tools remain refused. [Contract](CLAUDERIG-V2-PROCESS-LIFECYCLE.md#canonical-workflow-supervision-6b6b2d2). |
| 6b.6b.2d.3a. Linux/macOS fenced-store recovery | Merged: [#373](https://github.com/rigsmith/rigsmith/pull/373) | Durable ownership evidence, a pinned process group before command startup, and exclusive recovery after OS proof. Saved capture and queue state stay intact. [Contract](CLAUDERIG-V2-PROCESS-LIFECYCLE.md#unix-fenced-store-recovery-6b6b2d3a). |
| 6b.6b.2d.3b. Windows fenced-store recovery | Merged: [#374](https://github.com/rigsmith/rigsmith/pull/374) | Durable prelaunch/cleanup phases and kernel-incarnation evidence. Unconfirmed jobs require a verified kernel restart; legacy records stay blocked. [Contract](CLAUDERIG-V2-PROCESS-LIFECYCLE.md#windows-fenced-store-recovery-6b6b2d3b). |
| 6b.7a. Queue capacity and receipt compaction | Merged: [#375](https://github.com/rigsmith/rigsmith/pull/375) | Queue payload/headroom and remedy reporting; explicit replay cutoff, schema-3 receipt compaction, preserved unfinished work and monotonic generations. [Contract](CLAUDERIG-V2-QUEUE.md#queue-capacity-and-receipt-compaction-milestone-6b7a). |
| 6b.7b.1. Artifact-store capacity and interrupted archive writes | Merged: [#376](https://github.com/rigsmith/rigsmith/pull/376) | Optional sealed-byte admission limits, direct-file inventory and exclusive cleanup of interrupted archive writes. Existing artifacts remain reusable; seed substores inherit an independent limit. [Contract](CLAUDERIG-V2-CAPTURE-ARTIFACTS.md#store-capacity-and-interrupted-archive-writes-6b7b1). |
| 6b.7b.2a. Writer-owned workspace cleanup | Merged: [#377](https://github.com/rigsmith/rigsmith/pull/377) | Shared staging/capture/seed/commit ownership, bounded full inventory and confined removal of reserved capture-build, publication, startup and commit-store confirmation workspaces. Claude validates binding and source separation. Sealed artifacts, recovery stores, queue-parent confirmations and relocated OS-temp scratch remain protected. |
| 6b.7b.2b. Queue-aware archive reclamation | Merged: [#378](https://github.com/rigsmith/rigsmith/pull/378) | Queue worker/transaction ownership and durable reflush precede cleanup. Any unfinished work protects all archives; an idle queue with completed history can reclaim verified commits/captures/seeds only without recovery/unknown private state. Queue-parent confirmation scratch is included. Selective pruning while work remains is deferred; opt-in queued Claude rollout follows these checks. Reclamation wiring must first enforce exclusive queue/store lifecycle association. |
| 7a. Persisted Claude queue runtime | In review (current PR) | Fixed private stores, unique lifecycle queue binding, durable producer identity before enqueue, fresh resolver validation and saved-phase restart. [Contract](CLAUDERIG-V2-QUEUE-RUNTIME.md). |
| 7b. Explicit queued Claude commands | Next | Initialization, acceptance, supervised worker startup, status/retry and drain using existing Git/`gh` checks. |
| 7c. Opt-in hooks and rollback | Planned | Hook routing, synchronous coverage, stop/drain and rollback validation; ordinary synchronous behavior remains the default. |
| Codex adapter and separate `codexrig` executable | Planned | Consume the proven shared layers without moving Claude account/Desktop internals into them. |

Milestone 6b.2 is split above into the steps already delivered and the completed
chunk recovery step. The remaining recovery, coverage, lifecycle and cleanup gates
now have separate rows, so partial completion is visible. Canonical recovery is split into staged merge completion (6b.4a), capture integration (6b.4b.1), and unresolved recovery (6b.4b.2). Queued hooks remain disabled.

Restore boundary validation merged for [v1 (#339)](https://github.com/rigsmith/rigsmith/pull/339)
and [v2 (#340)](https://github.com/rigsmith/rigsmith/pull/340): preserve missing-destination no-ops, report unexpected link-operation failures with partial restore reports, and reject
unsafe manifest link endpoints before and after slug rewriting, and confine target
checks and link creation to the selected restore folder. Tests include older saved
metadata, traversal, external target symlinks, target-swap verification, no-replace installation and concurrent-destination preservation,
and a gated sync→Git→clone→restore shared-memory link round trip.

Native append recovery merged in [#341](https://github.com/rigsmith/rigsmith/pull/341): combine proven append-only native JSONL and
memory changes, preserve shared lines and unknown record payloads, and reject
conflicting UUIDs or edited history. Publication still audits the complete tree.
[#342](https://github.com/rigsmith/rigsmith/pull/342) added bounded canonical chunk-index recovery, including default chunked
backups. [#343](https://github.com/rigsmith/rigsmith/pull/343) adds ordinary-file snapshot ordering; staged canonical merge completion merged in [#344](https://github.com/rigsmith/rigsmith/pull/344); staged completion before fresh capture merged in [#346](https://github.com/rigsmith/rigsmith/pull/346). Private unresolved-merge planning merged in [#347](https://github.com/rigsmith/rigsmith/pull/347). Recoverable file/index application merged in [#348](https://github.com/rigsmith/rigsmith/pull/348); queue recovery integration merged in [#357](https://github.com/rigsmith/rigsmith/pull/357). Queued hooks stay disabled.

Merged [#345](https://github.com/rigsmith/rigsmith/pull/345) reuses the shared staging-state guard before fresh capture, blocks active bisects during capture and merge completion, and corrects a Windows chunk fixture whose fixed timestamp could hide rewritten bytes from Git. Step 6b.4b.1 merged in [#346](https://github.com/rigsmith/rigsmith/pull/346). Unresolved recovery is split into private planning, safe application/restart, and queue integration (6b.4b.2a–c).

## Delivery priority

Merged [#358](https://github.com/rigsmith/rigsmith/pull/358) runs the existing
Windows groups in parallel. Its final run completed the Windows groups in 8m59s,
compared with 37m22s in #357, preserving tests, limits and the aggregate merge gate.

1. **Merged in [#327](https://github.com/rigsmith/rigsmith/pull/327):** reuse existing Git/`gh` configuration for retained publication.
   Real Git/gh fixtures exercise the primary login workflow without new credential files,
   keychain configuration or a generic credential-provider layer. Preserve the
   current private-repository checks and existing synchronous workflows.
2. **Merged in [#332](https://github.com/rigsmith/rigsmith/pull/332):** connect the durable queue to the Claude capture, commit and
   publication services, with exact batch acknowledgement and saved-phase replay.
   Merged in [#336](https://github.com/rigsmith/rigsmith/pull/336): bounded retry/backoff and explicit failure blocking. Merged in [#337](https://github.com/rigsmith/rigsmith/pull/337): retained manifest/device
   conflict unions. Native JSONL/memory append recovery merged in [#341](https://github.com/rigsmith/rigsmith/pull/341). [#342](https://github.com/rigsmith/rigsmith/pull/342) added bounded chunk-index recovery. [#343](https://github.com/rigsmith/rigsmith/pull/343) adds ordinary-file snapshot ordering. Merged in [#344](https://github.com/rigsmith/rigsmith/pull/344): completion of already-staged canonical merges. Merged: [#346](https://github.com/rigsmith/rigsmith/pull/346), staged completion before fresh capture. Merged: [#347](https://github.com/rigsmith/rigsmith/pull/347), private unresolved-merge planning. Merged: [#348](https://github.com/rigsmith/rigsmith/pull/348), recoverable file/index application. Merged: [#357](https://github.com/rigsmith/rigsmith/pull/357), queue recovery integration. Merged: [#359](https://github.com/rigsmith/rigsmith/pull/359), shared manual-sync coverage checkpoints (6b.5a). Merged: [#363](https://github.com/rigsmith/rigsmith/pull/363), Claude per-request capture evidence and confirmed publication (6b.5b). Merged: [#365](https://github.com/rigsmith/rigsmith/pull/365), shared worker loop, controlled stop/drain and saved-phase restart (6b.6a). Merged: [#367](https://github.com/rigsmith/rigsmith/pull/367), startup shared-history validation (6b.6b.1). Merged: [#368](https://github.com/rigsmith/rigsmith/pull/368), Windows child ownership at creation (6b.6b.2a). Merged: [#369](https://github.com/rigsmith/rigsmith/pull/369), Unix worker-death supervision (6b.6b.2b). Merged: [#370](https://github.com/rigsmith/rigsmith/pull/370), persistent OS restart fencing (6b.6b.2c). Merged: [#371](https://github.com/rigsmith/rigsmith/pull/371), canonical Git command-runner plumbing (6b.6b.2d.1). Merged: [#372](https://github.com/rigsmith/rigsmith/pull/372), canonical workflow supervision (6b.6b.2d.2). Merged: [#373](https://github.com/rigsmith/rigsmith/pull/373), Linux/macOS fenced-store recovery (6b.6b.2d.3a). Merged: [#374](https://github.com/rigsmith/rigsmith/pull/374), Windows recovery (6b.6b.2d.3b). Merged: [#375](https://github.com/rigsmith/rigsmith/pull/375), queue capacity/receipt compaction (6b.7a). Merged: [#376](https://github.com/rigsmith/rigsmith/pull/376), artifact-store limits and interrupted archive writes (6b.7b.1). Merged: [#377](https://github.com/rigsmith/rigsmith/pull/377), writer-owned workspace cleanup (6b.7b.2a). Merged: [#378](https://github.com/rigsmith/rigsmith/pull/378), queue-aware archive reclamation (6b.7b.2b). In review: persisted queue runtime and producer attribution (7a). Next: explicit queue commands (7b), then opt-in hooks and rollback (7c). Command/hook wiring remains disabled. Worker ownership excludes concurrent execution throughout coverage capture, publication and acknowledgement.
3. Finish platform supervision and parent-death cleanup, validate OS startup/restart, capacity and
   artifact/receipt cleanup before enabling queued hooks.
4. Validate opt-in queued Claude sync and rollback against the synchronous path.
5. Build the separate `codexrig` adapter using the proven shared layers.

SSH-agent discovery, broad credential-helper support and new OS keychain
integration are deferred. They are not prerequisites for delivering the queue
or adding a second vendor. The merged queue, retained commits, remote
confirmation and owned process cleanup remain part of v2.

After v1.15.1, [#299](https://github.com/rigsmith/rigsmith/pull/299) brought the
foundation into main and [#301](https://github.com/rigsmith/rigsmith/pull/301) merged
main forward into `codex/v2`. [#302](https://github.com/rigsmith/rigsmith/pull/302)
and [#303](https://github.com/rigsmith/rigsmith/pull/303) aligned the end-user
changesets. The integration branches share that foundation; #304 subsequently added the
Claude adapter to v2 only. Merged into a branch does not mean released in a binary.

For the implemented contracts, see [compatibility](CLAUDERIG-V2-COMPATIBILITY.md),
[services](CLAUDERIG-V2-SERVICES.md), [the adapter](CLAUDERIG-V2-ADAPTER.md), and
[shared file mechanics](CLAUDERIG-V2-FILES.md), and
[records/publication](CLAUDERIG-V2-RECORDS-PUBLICATION.md).

Keep this progress table and the [root roadmap](../roadmap.md) current in each
implementation PR. Mark work merged only after the PR merges, and record release
status separately. The numbered sections below retain the detailed completion
gates; their milestone numbers are not GitHub PR numbers.

**Compatibility contract**

The extraction phase must preserve:

- Existing commands, aliases, flags, exit behavior, prompts, and hook commands. Keep manual sync synchronous.
- `~/.clauderig`, configured roots, staging locations, account/profile stores, credential storage, and environment overrides.
- Existing backup paths and formats: root IDs such as `cli`, `desktop`, and `desktop@…`; manifest filename/schema; device and ledger records; redaction sentinel; `main` and `config-history` branches.
- Allowlist decisions, path correction, symlink protections, permissions, timestamps, retention, throttle thresholds, scoped final flushes, merge policies, and restore backup/prune behavior.
- Existing attribution rules, including unknown identity and the distinction between account and Desktop profile.
- Current configured fresh-machine auto-restore during `pull`. The command is not strictly staging-only when that option is enabled. See [auto-restore](../internal/clauderig/service/pull.go).

Compatibility here means measured equivalence, not a promise of zero possible regressions. Each extraction PR needs focused regression evidence and must be revertible without migrating user data. Known defects should be recorded and fixed in separate changes; they should not be silently enshrined as desired behavior.

**Milestone sequence and completion gates**

| Milestone | Scope | Completion gate |
|---|---|---|
| 1 | Establish the compatibility fixture suite and baseline | Old and new builds can be compared against isolated fixtures; synthetic round-trip tests run in CI. |
| 2 | Extract synchronous application services from command handlers | CLI behavior and failure ordering remain equivalent; commands call services and render results. |
| 3 | Extract root/file classification and explicit flush intent | Claude adapter selects the same artifacts, retention, and flush coverage. No storage migration. |
| 4 | Extract generic file processing and restore mechanics | Identical staged payloads and restored trees; legacy JSON and raw-file policies preserved. |
| 5 | Add session/metadata and Git publication boundaries | Claude serialization and conflict policies remain authoritative; no cross-vendor assumptions in shared orchestration. |
| 6 | Add store coordination, then durable queue infrastructure | Competing operations serialize, crash/retry tests pass; installed hooks still use the current path. |
| 7 | Offer queued Claude sync as an explicit feature | Queued and synchronous runs converge; service lifecycle, rollback, and startup behavior are validated. |
| Later | Extract guard/customization helpers as Codex consumes them | Each extraction has a second concrete consumer and preserves Claude-specific semantics. |

Each row is a reviewable milestone, not necessarily one large commit. Split broad rows into mechanical moves and wiring changes where that makes review easier.

**Milestone 1 — make compatibility observable**

Completed: CI enables synthetic end-to-end tests with `CLAUDERIG_E2E` and the pinned command comparison with `CLAUDERIG_COMPAT` on Linux, macOS, and Windows. The real-user-data scan remains separately gated and disabled in CI. The fixture contracts below remain the extraction gate. See [CI](../.github/workflows/ci.yml) and [round-trip tests](../internal/clauderig/e2e/e2e_test.go).

Create small synthetic fixtures representing:

- User/CLI roots, Desktop sidecars, multiple Desktop profiles, and account attribution.
- Settings with local secrets, instructions/skills, ordinary and subagent transcripts, durable memory, symlinks, and excluded caches.
- Old manifests and ledgers, missing roots, malformed/partial records, file-directory collisions, and version skew.
- Retention boundaries, oversized files, throttled growth, and all flush-input cases.
- Offline push, retry with no new local changes, divergent staging branches, abandoned merge, and history squash.
- Established versus fresh machines with auto-restore enabled/disabled, explicit restore backup, and prune protection.

Compare an unchanged baseline binary and the refactored binary in separate temporary homes/staging trees against local bare remotes. Compare file bytes and metadata, Git tree contents, relevant refs, exit statuses, and meaningful messages. Normalize only nondeterministic fields such as timestamps and temporary paths. Do not compare commit hashes when time or fixture paths differ. Never shadow-run two mutating pipelines against the user's live tree or remote.

**Milestone 2 — separate execution from Cobra and presentation**

Start in `internal/clauderig/service`, keeping everything Claude-specific initially. Extract the sync/pull/reconcile/publication workflow currently embedded in command handlers into callable services. Commands retain argument parsing, stdin interpretation, interactive approvals, and terminal rendering. Services accept explicit request/context values and return results plus progress events.

Preserve execution order. In particular: repair an abandoned merge before staging; capture identity once; stop publication after a tripwire failure; retry a previously failed push even when there is no new commit; retain existing best-effort versus fatal error behavior. Current `sync --dry-run` stages/scans but does not commit or push—do not accidentally redefine it as a read-only plan.

Keep the engine's existing separation from Git. A future queue worker calls the same service as manual sync; it should not shell out recursively through Cobra or scrape terminal output. Add dependency seams for clock, filesystem roots, identity lookup, and transport only where needed for these contracts. Avoid wrapping the entire Go standard library.

**Milestone 3 — replace hidden path assumptions with a Claude adapter**

Introduce an internal root/artifact descriptor with explicit kind, source root, relative path, retention class, transform selection, merge selection, and flush grouping. Have the Claude adapter produce the current answers for `projects/`, memory, sidecars, and profile roots. The descriptor is initially in-memory only.

Explicit flush intent is already implemented by milestone 2. Preserve its service-boundary contract: normal, selected sessions, or all. Preserve current command decoding: manual flush with no hook payload means all; a valid hook payload flushes that session and its subagents; malformed or blank hook payload flushes none and reports why. Keep this decision out of the generic worker.

Keep Claude's defaults in its adapter. Generalize the allowlist matcher separately from the Claude allowlist itself. Do not infer transcript or merge semantics from `.jsonl`, or broaden allowed roots during extraction.

**Milestone 4 — extract mechanics while retaining Claude policy**

Move proven generic pieces into a small `internal/agentrig` package family: file walking/copying, staging reconciliation, result types, and restore mechanics. Reuse `core/pathmap` and `core/gitrepo` as they stand. Inject artifact classifications and codecs rather than importing `internal/clauderig` from shared code.

Keep the existing JSON serializer, secret merge semantics, redaction marker, file mode handling, and path transforms behind the Claude codec. Protect symlink ancestors and refused destinations during prune exactly as today. A codec boundary should support TOML later, but this PR does not implement it.

The preparatory secret-scanning and transcript-chunking work is complete. Any further detection or rewriting changes need separate security work and sanitized fixtures before changing defaults. Do not combine stricter secret rules, transcript rewriting, or new merge policy with mechanical extraction. Codex's TOML support must use structured processing before it is shipped; it must not inherit the raw-file fallback.

**Milestone 5 — separate common records from native persistence**

Use a normalized session summary for the ledger/search orchestration: native ID, title, project/cwd, activity, provenance, and optional account/profile attribution. Claude readers continue to interpret transcripts and sidecars. Claude serializers continue to write today's ledger, manifest, and device records unchanged. An internal vendor field does not require adding one to every existing stored record.

Extract Git publication coordination around the existing `core/gitrepo` operations. Supply merge handlers, metadata serializers, branch names, and history selection through the Claude adapter. Keep today's `config-history` selection and squash behavior; swapping a hardcoded path for a descriptor must not change what lands on that branch.

Only extract the part of search that consumes normalized records. Resume, move/relink, account storage, and Desktop launching can remain Claude implementations. The shared layer must not assume account IDs are UUIDs or that all session logs can be union-merged.

**Milestone 6 — coordination and durable work, with no automatic activation**

Treat concurrency control as a separately tested behavior change. Establish one store-level coordination contract across manual sync, pull/reconcile, restore, and the future worker. Account credential locks do not substitute for staging-store coordination. Use canonical store identity and verify that alias paths do not let two writers bypass the same lock. Acquire once per operation; nested service calls must not deadlock. Keep live-account locking separate.

Then add a durable queue and worker in the shared package, with a `clauderig` command entrypoint and queue state under its own local state directory, excluded from sync. A file spool with atomic writes is a reasonable initial implementation. Define crash durability and stale-owner recovery on every supported OS; a temporary-file rename by itself is not the whole design.

Jobs need explicit store/root identity, event/session identity, flush intent, enqueue time, retry state, and an acknowledgement generation. Account provenance must be captured or revalidated against the source actually captured: a delayed job must not attribute yesterday's session to whichever account is active when it runs. A later config edit must not silently redirect an existing job to a different remote or root.

Required cases: duplicate events, events arriving during a running snapshot, queue replay after crash, transient transport failure, permanent scan failure, and successful local commit followed by failed push. Finish only the generation actually captured; preserve later work. Coalescing must union selected flushes, and retries must remain idempotent. Never automatically resolve a blocked job by dropping its requested data.

Track queued/captured/committed/pushed separately. Protect pending captures from retention and handle source deletion explicitly. Manual sync must coordinate with the worker and acknowledge only queue work its capture actually covered. Queue schema versions and worker ownership need a defined compatibility/upgrade policy.

**Milestone 7 — adopt the queue gradually**

Keep existing installed hooks and synchronous commands operational. Offer an explicit install/configuration mode for queued Stop/SessionEnd capture after worker lifecycle is supported. A detached child alone does not guarantee retries after logout or reboot; add supported service registration or a clearly specified restart/reconciliation mechanism. No new mandatory background process for existing users.

Keep the SessionStart pull/fresh-machine bootstrap on the existing synchronous path initially. Enqueueing it and immediately starting Claude would change when restored settings and sessions become available. Moving that path later requires a readiness/completion contract, not just changing the hook's command string.

Validate queued operation on disposable fixtures, then an explicitly enabled installation. Include worker restart, offline recovery, queue saturation, final transcript removal, and active-session churn. A hook must acknowledge durable work quickly and keep vendor-specific stdout separate from human-readable progress.

Rollback must restore direct hook commands, stop or drain the worker without racing foreground operations, and preserve pending jobs and local snapshots. Because backup payloads retain their existing formats, rollback must not require restoring a backup-repository migration. Leave the default unchanged until compatibility and operational evidence justify a separate rollout decision.

**Defer these changes**

Keep account credentials, OS keychains, Desktop profiles, launching/deep links, directory moves, and Claude transcript interpretation in `internal/clauderig` until there is a proven second implementation. Guard policy can be extracted later while its Claude tool registry, input/output, messages, and overrides remain unchanged. Managed instruction blocks and MCP descriptions are similarly small later extractions.

Do not combine this work with transcript chunking, a unified backup repository, renamed manifests/config, automatic cross-vendor session conversion, a new UI, or a whole-package relocation. Each adds migration or behavior risk without being necessary for shared code.

The target dependency direction is:

```text
cmd/clauderig -> Claude commands/composition -> shared services <- Codex commands/composition <- cmd/codexrig
                         |                          |                       |
                   Claude adapters          core Git/path/worktree    Codex adapters
```

The composition code passes adapter implementations into narrow interfaces owned by the shared services. Shared packages import neither vendor package. Keep these interfaces internal until both vendors validate them; a public SDK or external plugin ABI would freeze guesses too early.

Milestones 1–5 are merged; milestone 5 landed in v2 through #307. Milestone 6a merged in #308. Milestone 6b.1 merged in #309; 6b.2 has the shared execution driver and Claude service split merged in #310; durable artifacts and capture/sealing merged in #311. Retained commit bundles and the Claude commit adapter merged in #315. Capture-time seed retention merged in #317. Shared retained publication merged in #318. Claude retained publication policy merged in #319. Owned process cleanup merged through [#323](https://github.com/rigsmith/rigsmith/pull/323) and [#324](https://github.com/rigsmith/rigsmith/pull/324). [#326](https://github.com/rigsmith/rigsmith/pull/326) removed the custom network transports and proposed credential helper, retaining the local fixture adapter. Existing Git/`gh` publication integration merged in [#327](https://github.com/rigsmith/rigsmith/pull/327). Claude queue execution and retained-phase recovery are merged in [#332](https://github.com/rigsmith/rigsmith/pull/332). The delivery order now prioritizes queue recovery/lifecycle, queued Claude sync and then the Codex adapter; broad credential discovery is deferred. Milestones 3–5 establish the shared foundation; milestones 6–7 deliver the queue as a separately controlled feature. `codexrig` can begin using the proven boundaries without requiring Claude account/Desktop internals to move.

Baseline check during this roadmap: `CLAUDERIG_E2E=1 go test ./internal/clauderig/e2e -run '^TestE2E_(RoundTrip|CrossOSPortability)$' -count=1 -v` passed, including both macOS→Windows and Windows→macOS path-mapping cases on this macOS host. These are synthetic fixtures and local bare remotes, not tests of live Claude resume on Windows. No implementation or runtime configuration was changed.
