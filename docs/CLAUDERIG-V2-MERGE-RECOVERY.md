# V2 queued merge recovery

Milestone 6b.4b.2c connects supported unresolved merge recovery to fresh capture
and retained publication. Queued hooks remain disabled. The existing synchronous
commands keep their behavior; this change affects the queue artifact services.

`MergeStageStore.Complete` extends the [staging protocol](CLAUDERIG-V2-MERGE-STAGING.md)
through the exact saved merge commit and Git metadata cleanup. Its immutable,
durable intent contains the candidate commit before any canonical modification.
That record bridges the crash windows between staging, HEAD update, cleanup and
the caller's next durable queue marker; it is not a remote-publication receipt.

## Completion and restart

The caller holds the cooperative staging lease throughout, including child
cleanup. The same intent-owned index lock spans staging through completion.
Retries reflush and verify the intent, re-audit both parent tips and candidate,
and reuse saved resolutions without consulting the resolver again.

Before HEAD changes, the existing staging checks require the saved merge state,
before/after index and affected file bytes. Completion compare-and-swaps HEAD to the saved candidate itself, then uses
`FinishStagedMerge` for audited cleanup. It does not recreate the commit under
canonical repository encoding settings. After HEAD changes, a
retry requires that exact commit, the same symbolic branch (or expected detached
HEAD), original parent marker, saved resolved index and resolved affected files.
Any remaining MERGE_HEAD or AUTO_MERGE must still identify the saved operation.
Missing merge metadata is accepted only at the exact saved commit. Partial Git
cleanup can then finish without aborting the merge or restoring old files.

Changed HEAD, a different branch at the same commit, changed index, affected
edits, foreign locks and other active operations are refused. No ancestry-only
test authorizes completion, and no rollback overwrites later work. Unrelated
unstaged/untracked files remain untouched. Completed replay verifies canonical
history as well as the retained bundle. An error never authorizes advancement of
the queue phase. The existing Git reference-update semantics apply; this is not
a transaction spanning Git refs, its operation metadata and queue storage.

## Service checkpoints

Each capture key has a dedicated `merge-recovery-<key>` directory in its capture
store. Each retained commit key has one in its commit store for publication.
Those keys bind immutable batch membership and producer/configuration identity;
mutable retry counts do not select a new checkpoint. The saved intent also binds
the canonical Git directory, merge policy and commit identity.

Services probe the exact expected sealed artifact, not just its directory.
Empty work directories from failed planning allow later manual resolution; a
present corrupt or nonregular artifact is still a checkpoint and must be refused
by verification. Services consult that checkpoint even when Git reports settled
HEAD. This prevents a restart from silently accepting a different completed merge.
Without a checkpoint, already-staged manual resolutions retain the existing
completion path. Supported unresolved conflicts invoke the native Claude merge
policies through `Complete`; unsupported states remain blocked.

Capture must retain the completed ancestry and seal its capture before the queue
advances. If source capture or seed retention fails, the merge and its checkpoint
remain for retry. Reusing a sealed capture skips recovery and source reads.
Publication verifies the retained batch before canonical recovery, then publishes
with the existing Git/gh transport. Offline retries keep the committed batch and
its repair checkpoint; only confirmed publication permits acknowledgement.
Newer queued generations remain pending.

On its first recovery attempt, publication deliberately observes the current
canonical merge, which can be newer than the retained capture. This matches the
publisher's existing `LocalCommit` contract for newer synchronous history. The
private plan is audited and sealed before any canonical write; that intent then
pins all subsequent attempts. The recovered commit is combined with the retained
capture through normal publication merging, never substituted for it or used to
acknowledge newer events. Unrelated histories are not forcibly replaced. Once an
intent exists, a different canonical HEAD or operation is refused on retry.

Checkpoints must remain until their batch no longer needs replay. Deletion,
abandoned-attempt recovery, total temporary-history quotas and cleanup of crash
leftovers remain the capacity milestone. The existing stable-filesystem and
cooperative-writer contract applies; arbitrary parent swaps are excluded. Audits
cover the two parent tips and candidate, not every historical ancestor.

Synthetic tests include process exits after file/index writes, HEAD update and
completed cleanup; SHA-1/SHA-256 and detached HEAD; changed-state refusals; partial
metadata cleanup; and capture/publication queue retries after source or transport
failure, including preservation of newer events.
