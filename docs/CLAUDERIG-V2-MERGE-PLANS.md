# V2 unresolved merge planning

Milestone 6b.4b.2a adds an internal shared planner for unresolved canonical merges.
It does not apply a repair, move canonical HEAD, acknowledge queue work, or enable
queued hooks. Capture and publication now recover supported canonical conflicts
through the [sealed completion protocol](CLAUDERIG-V2-MERGE-RECOVERY.md).
Recoverable application is described in [merge staging](CLAUDERIG-V2-MERGE-STAGING.md);
queue integration uses that application path. The planner itself remains preview-only.
There is no end-user changeset for this internal planning foundation.

## Inputs and ownership

`commitartifact.PlanUnresolvedMerge` takes a canonical repository, a new output
directory, and vendor-supplied content resolution, validation and audit callbacks.
The caller holds the cooperative staging lease throughout. The output directory's
parent must exist; the output must not exist or overlap the checkout, its Git
directory, its shared Git directory, or any other registered checkout. An
unavailable registered checkout fails closed. Destination
parent symlinks are resolved before this check. Filesystem identity checks on
existing destination ancestors also reject aliases such as Unicode normalization. Arbitrary concurrent filesystem
writers and parent swaps outside the lease contract are not supported.

The initial implementation accepts one literal `MERGE_HEAD`, a matching
`ORIG_HEAD` and current HEAD, and unresolved regular-file content/add-add conflicts.
Autostash, bisect, cherry-pick, revert, rebase and sequencer states are refused.
Partially staged resolutions, additional staged edits, mode/delete conflicts and
index entries differing from the private reconstruction remain blocked. Existing
staged-only completion remains the appropriate path for fully resolved merges.

The planner copies the index for inspection and imports the exact committed parent
histories into a private bare repository. It forces Git's built-in text driver and
disables rename inference. It reconstructs all stage-zero and conflict entries,
then compares that listing with the copied canonical index. This avoids treating
manual staged edits as disposable merge output. Worktree bytes are not used to
infer resolutions and are never modified; a later live edit does not invalidate a
preview, but must be checked before application.

## Candidate and audits

The existing `ResolveConflict` and `RelatedFiles` contracts resolve supported
conflicts from immutable blobs and companion files. Additive companion proposals
support policies such as transcript chunk creation. Vendor rules remain in their
existing packages. A gated synthetic test uses Claude's native append policy and
byte-preservation/secret checks; edited history and secret-bearing inputs fail.

Both parent-tip trees and the candidate tree pass raw path/mode validation, vendor
validation and secret auditing. Audit callbacks cannot silently modify the tree:
raw hashes and directory membership are verified afterward. This does not scan or
certify every historical ancestor; the complete retained-history policy remains
unchanged. The candidate commit has the exact original and incoming parents in
that order, with the supplied identity and event time.

The planner exports `merge.bundle` containing `refs/rig/merge-plan`, then imports
it into an empty verification repository and checks complete history. It rechecks
canonical HEAD, index bytes, parent markers and unsupported operation state before
returning. It never inserts candidate objects into the canonical object store.
Configured hooks, filters, merge programs and filesystem monitors are not invoked.

`MergePlan` returns the original/incoming commits, candidate commit/tree, SHA-256
of the observed index bytes and bundle path. The bundle is independent of the
source repository. It is a caller-owned preview, not a durable artifact reference,
queue receipt, or permission to install a stale plan. No fsync or crash-persistence
guarantee is made. The caller removes the output when finished; ordinary failures
attempt to remove only the directory created by this call.

## Limits and remaining work

Index snapshots are bounded to 64 MiB; Git control listings to 1 MiB. Existing
content resolution limits remain 128 conflicts, 1 MiB per blob/result and 16 MiB
combined. Companion reads/additions retain their separate bounded contract.
`MaxTreeBytes` bounds each materialized tree; `MaxBundleBytes` bounds streamed
bundle output. Zero uses the existing archive default for either byte limit.
These bounds do not provide a total temporary-disk quota for imported Git history.

Applying a plan requires fresh provenance checks and explicit handling of edited
conflict files, modes, untracked collisions, index flags and interrupted writes.
The returned index digest alone does not certify worktree state. Queue services
use `MergeStageStore.Complete` to persist the installation intent, apply the
repair and resume exact-candidate completion through Git metadata cleanup.
No partial-resolution or arbitrary external-writer guarantee is implied by this
planner. Queued hooks remain disabled; synchronous commands keep their behavior.

Tests cover both Git object formats, split indexes, raw binary/CRLF results,
companion proposals, nonconflicting entries, complete detached bundles, unchanged
canonical state, configured-program isolation, changed-state and audit refusal,
capacity/cancellation cleanup, linked worktrees and destination ownership.
