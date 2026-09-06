# ClaudeRig v2 application services

This is the first part of the synchronous-service milestone, on `codex/v2`.
It prepares callable workflows for the later queue and Codex adapter while
keeping ClaudeRig and CodexRig as separate tools. No background worker, vendor
adapter, storage migration, or runtime setting is introduced here.

## Current boundary

`internal/clauderig/service` owns these existing workflows:

| Entry point | Responsibility |
| --- | --- |
| `Service.Publish` | Audit the captured store, commit, retry push through reconciliation, maintain config-history, then repack/squash when required. |
| `Service.Reconcile` | Fetch/merge, apply Claude conflict policies, optionally invoke an explicitly permitted mergetool, and audit before completing the merge. |
| `Service.RepairMerge` | Settle an abandoned merge before a caller captures data; report whether the store is safe to write even when an attempted reconciliation failed and aborted. |
| `FinishMerge` | Preserve the staged/working-copy consistency check, byte-preservation attributes, and publication audit for both manual and automatic merges. |
| `Service.Pull` | Preserve best-effort clone/pull/reconciliation and the configured fresh-machine auto-restore behavior. |

Requests carry resolved paths, configuration, machine identity, and permission to
invoke mergetool. The services do not inspect the terminal or import Cobra or
terminal styles. Progress is delivered synchronously as typed events; commands
render the existing messages. Results expose publication phases and best-effort
pull failures without requiring another caller to parse terminal text.

The observer is informational: it must not mutate the store or reenter a service.
The optional `Now` clock controls the maintenance cutoff and defaults to the
existing real-time behavior. Git and filesystem operations still use the existing
implementations; this extraction does not introduce a second transport stack.

## Preserved execution contracts

- Sync holds its existing lock across repair, capture, and publication. Services
  do not acquire another lock. Pull retains its existing coordination behavior;
  store-wide coordination remains a separate milestone.
- The command repairs abandoned merges before `engine.Sync` writes the snapshot.
  Identity is still captured once and shared by ledger and device metadata.
- Hook payload decoding, debounce, flush behavior, and dry-run stay in the command.
  Dry-run still stages and scans without publication or a sync journal entry.
- Capture journalling and device registration still happen before publication;
  Git-phase failures are still journalled by the command.
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
Core Git tests continue to cover history rewriting itself, including merge
history and out-of-order commit dates.

## Next extraction

Finish the synchronous application boundary by moving capture orchestration,
identity/device updates, and journal ownership behind a callable sync entry
point. Preserve the command's input and failure ordering while doing so. Then
introduce explicit artifact classification and flush intent before extracting
vendor-neutral mechanics. Shared store coordination and the durable queue follow
those boundaries; this package alone is not yet a complete queue-worker API.
