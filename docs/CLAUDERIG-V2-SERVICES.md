# ClaudeRig v2 application services

This completes the synchronous-service extraction milestone, shared by `main`
and `codex/v2` after v1.15.1. These services have no Codex runtime dependency.
They prepare callable workflows for the later queue and Codex adapter while
keeping ClaudeRig and CodexRig as separate tools. No background worker, vendor
adapter, storage migration, or runtime setting is introduced here.

## Current boundary

`internal/clauderig/service` owns these existing workflows:

| Entry point | Responsibility |
| --- | --- |
| `Service.Sync` | Repair before capture, observe identity once, capture/scan, journal the outcome, update devices, and publish. |
| `Service.Publish` | Audit the captured store, commit, retry push through reconciliation, maintain config-history, then repack/squash when required. |
| `Service.Reconcile` | Fetch/merge, apply Claude conflict policies, optionally invoke an explicitly permitted mergetool, and audit before completing the merge. |
| `Service.RepairMerge` | Settle an abandoned merge before a caller captures data; report whether the store is safe to write even when an attempted reconciliation failed and aborted. |
| `FinishMerge` | Preserve the staged/working-copy consistency check, byte-preservation attributes, and publication audit for both manual and automatic merges. |
| `Service.Pull` | Preserve best-effort clone/pull/reconciliation and the configured fresh-machine auto-restore behavior. |

Requests carry resolved paths, configuration, machine identity, and permission to
invoke mergetool. The services do not inspect the terminal or import Cobra or
terminal styles. Progress is delivered synchronously as typed events; commands
render the existing messages. Results expose publication phases and best-effort
pull failures without requiring another caller to parse terminal text. Missing
configuration is rejected before filesystem work: sync returns an error; pull
sets `RequestError` and emits `PullFailed`.

The observer is informational: it must not mutate the store or event payloads,
or reenter a service. `ReadIdentity` defaults to the existing live-account reader
and is called once per sync. The optional `Now` clock controls device timestamps
and the maintenance cutoff, defaulting to the existing real-time behavior. Git and filesystem operations still use the existing
implementations; this extraction does not introduce a second transport stack.

## Preserved execution contracts

- Sync holds its existing lock across repair, capture, and publication. Services
  do not acquire another lock. Pull retains its existing coordination behavior;
  store-wide coordination remains a separate milestone. Lock ownership tokens
  include a random suffix so same-timestamp acquisitions remain distinct; stale
  lock parsing still accepts existing PID/timestamp files.
- `Service.Sync` repairs abandoned merges before `engine.Sync` writes the snapshot.
  Identity is still captured once and shared by ledger and device metadata.
- Hook payload decoding, debounce and locking stay in the command. `SyncRequest`
  carries explicit normal/selected/all flush intent and dry-run selection. The
  CLI supplies `ResolveFlush` so decoding still happens after merge repair and
  identity observation; direct callers can pass an already decoded intent.
  Dry-run still stages and scans without publication or a sync journal entry.
- The sync service owns capture journalling and device registration before
  publication, plus the separate Git-phase failure record. A capture refusal
  produces one refusal record; an offline push preserves the successful capture
  record in the commit and appends its failure record for the next sync.
- A new commit is not required for push retry. The service audits before commit
  and each push, and reconciliation audits before completing its merge.
- Publish reports success before history maintenance, matching existing output
  ordering. A later maintenance failure still returns an error; its result keeps
  the completed commit/push phases.
- Config-history selection, its 200-commit bound, push retries, repack-before-
  squash ordering, and local-midnight retention cutoffs remain unchanged.
- Pull never enables mergetool. Operational errors keep the existing best-effort
  command exit behavior. A failed initial clone does not create a journal that
  would obstruct the next clone. Fresh-machine restore remains opt-in, and an
  established machine's projects prevent it.

## Validation

The [pinned compatibility suite](CLAUDERIG-V2-COMPATIBILITY.md) compares old and
new CLI workflows in isolated homes with local remotes. Existing command tests
continue to cover abandoned merges, unsafe/conflicted publication, and hook
behavior through the command-to-service wiring.

Direct service tests add callers without Cobra and verify pending-commit retry,
secret refusal before commit, failed-clone destination handling, publication
phase events, config-history selection, and maintenance with a fixed cutoff.
Sync-service tests additionally verify repair-before-identity/input ordering,
a single identity observation shared by ledger and device records, dry-run
capture without publication, and journal ownership across capture refusal and
transport failure. Core Git tests continue to cover history rewriting itself,
including merge history and out-of-order commit dates.

## Artifact policy extraction on v2

V2 now supplies explicit root and file policies through
`internal/clauderig/adapter`: root selection, retention, transforms, merge
selection, and session/subagent flush grouping. The engine and merge resolver
consume those policies while retaining their existing implementations. See
[the adapter contracts](CLAUDERIG-V2-ADAPTER.md).

Next, extract the proven vendor-neutral file processing and restore mechanics. Store-wide
coordination and the durable queue follow: callable services are available now,
but they do not yet provide worker ownership, durable scheduling or retries
across process restarts. CodexRig remains a separate consumer to add after those
shared boundaries are established.
