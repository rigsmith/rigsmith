# V2 recoverable merge staging

Milestone 6b.4b.2b adds `commitartifact.MergeStageStore.Stage`, an internal shared
application path for the private merge planner. It stages the resolved tree and
updates only affected worktree files. It leaves HEAD and merge metadata in place
for the existing `FinishStagedMerge` completion step. Queue-service integration
and queued hooks are still disabled; there is no end-user changeset.

## Ownership and accepted state

The caller holds the cooperative canonical staging lease, uses stable local
filesystem directories, and supplies a dedicated private intent directory for
one merge attempt. The directory must be outside all registered checkouts and
Git metadata; the planner's symlink, filesystem-identity and root containment
checks apply. Arbitrary external writers and parent replacement outside the lease
contract are excluded. The existing Git runner isolates configuration, hooks,
filters and filesystem monitors.

A new repair requires the planner's exact unresolved two-parent index and Git's
saved `AUTO_MERGE` tree. The saved tree must match every nonconflicting index entry;
only unresolved paths may contain different marker blobs. Only regular-file
content/add-add conflicts and additive companion files are supported. Partial
resolutions, extra staged changes, missing `AUTO_MERGE`, mode/delete conflicts,
assume-unchanged and skip-worktree entries remain blocked. Split indexes are read
through the canonical repository and replaced with a complete resolved index.

Every affected live file must initially match its `AUTO_MERGE` bytes and Git
executable mode. A proposed new companion must be absent, even if an untracked
file has identical bytes. Symlink ancestors and nonregular targets are refused.
Unrelated unstaged and untracked files are preserved. Raw bytes are written
without Git attribute conversions. Windows cannot represent Git executable modes
in the same way as Unix; the index retains them, and live mode checking is limited
to platforms that expose those bits.

## Durable intent and application

Before any canonical object, file or index write, the existing artifact store
seals and flushes an immutable intent containing:

- Canonical Git-directory binding, exact parent commits, HEAD/merge markers and
  `AUTO_MERGE` tree identity.
- The verified candidate bundle and tree, with the planner's original event
  identity and a caller-supplied vendor policy version.
- Exact before/after indexes with digests, affected-file metadata and retained
  before/after file bytes.

Retries reflush the existing archive and reuse it without calling the resolver.
The archive checksum, binding and candidate ancestry are checked, the candidate
and both parent-tip trees are audited again, and the retained after-index must
produce that audited tree. These checks do not certify every historical ancestor.
A corrupt archive is refused rather than replaced.

Application takes `index.lock` exclusively. An unrelated lock is never removed.
A leftover lock bearing this exact sealed intent's token can be adopted after the
caller has reacquired the staging lease and established that its previous process
is no longer active. Unknown or partially written locks require deliberate
recovery; elapsed time alone is not evidence of ownership.

All affected files are checked before the first replacement. The candidate's
objects are imported without moving refs, files are installed by durable atomic
replacement, then the complete index is durably replaced last. Git operation
state and the index are checked between writes. Existing target files are
reflushed on retry rather than acknowledged solely from a successful read.

## Restart and refusal

An interrupted repair may leave some resolved files beside the original index.
The durable intent is retained, and retry accepts only each file's exact recorded
before or after state. The index must be byte-for-byte the recorded original or
resolved index. If the index is already resolved, every affected file must also
be resolved. Changed parent markers, HEAD, `AUTO_MERGE`, index bytes, file contents,
modes or newly introduced operations cause refusal; no rollback overwrites those
newer changes. Edits that recreate an exact recorded state are indistinguishable
from that state.

Successful staging returns the resolved tree identity. The caller next completes
the merge with `FinishStagedMerge` and retains durable phase state. Calling Stage
again after merge completion is refused. Bridging those phases, choosing intent
lifetimes, queue acknowledgements and recovery of interrupted completion belong
to milestone 6b.4b.2c; this PR does not activate that integration.

The intent store's `MaxBytes` bounds the complete archive. Planner tree, bundle,
conflict and companion limits remain in force; intent metadata is limited to
1 MiB and 1,024 affected files, and each index to 64 MiB. There is no total quota
for temporary imported Git history. Abrupt process exits may leave private work
folders or temporary replacement files; cleanup is part of the later capacity
work. Retained intents must not be deleted while a repair needs replay.

Tests cover both object formats, split indexes, linked worktrees, raw bytes and
native Claude append policy, later-edit refusal, foreign locks, policy and
capacity failures, interrupted file/index writes, and actual child-process exits
that leave an owned index lock for restart recovery.
