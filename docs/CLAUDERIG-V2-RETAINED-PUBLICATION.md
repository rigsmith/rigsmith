# V2 retained publication

Milestone 6b.2 now has an internal shared publication engine in
`internal/agentrig/commitartifact.Publish`. It consumes the durable commit bundles
from #315/#317, merges committed history in a private repository, and confirms
remote ancestry before returning success. This is a library boundary with real
local-Git transport tests. Claude now supplies an internal `Service.PublishArtifact`
policy adapter with an explicitly injected bound transport. The custom HTTPS/SSH implementations are removed in
[#326](https://github.com/rigsmith/rigsmith/pull/326); the
[local Git adapter and owned process runner](CLAUDERIG-V2-GIT-TRANSPORT.md) remain.
`NewConfiguredGitTransport` now delegates remote operations to existing Git
configuration, including `gh` credentials for the primary GitHub path. Queue execution and bounded retry
policy are integrated; lifecycle and further recovery gates remain pending.

## Inputs and ownership

The caller supplies the exact committed artifact and capture references from the
sealed batch, an explicit destination/branch transport, optional canonical
repository/commit, deterministic merge identity and timestamp, a bounded attempt
count, and mandatory Validate/Audit callbacks. It must validate the queue binding,
event membership and provenance before acting and hold staging ownership until
all work and child processes finish. Transport authentication must not select a
vendor account or change the bound destination.

The engine opens and verifies the existing commit bundle; it never calls Build
or loads live capture sources. A missing or corrupt committed artifact blocks
publication. The descriptor's CaptureRef must equal the expected reference.
The optional local input imports only an exact committed SHA. Canonical refs,
working files, index and configuration remain untouched, including staged edits.
Already-confirmed remote work can finish without importing a missing local seed.

Transport.Fetch must distinguish a confirmed absent branch from offline or
unauthorized access, import complete history into the supplied private ref and
return its exact SHA. The engine clears that ref before each fetch, checks the
returned ref/SHA and object integrity, and refuses shallow history. Transport.Push
must send the exact candidate to the bound branch using a normal fast-forward-only
push. There is no force push, remote-config lookup or history maintenance here.
Transport implementations are trusted code, not a sandbox. `GitTransport` implements this contract for local fixtures and, through
`NewConfiguredGitTransport`, existing Git configuration for network publication. Its [contract](CLAUDERIG-V2-GIT-TRANSPORT.md) records the
existing Git/`gh` integration direction and command ownership limitations.

## Merging and byte checks

The engine merges the retained capture with the supplied local commit, then with
the freshly observed remote tip. Ancestor relationships take the appropriate
existing commit. Divergence uses Git's `merge-tree --write-tree`, followed by a
commit with explicit parents, identity, timestamp and message. Identical inputs
produce the same candidate after an interrupted attempt. Git must support that
merge-tree mode; failure is surfaced, with no fallback to a checkout merge.

Conflicts fail closed with ErrConflict. Unrelated history, invalid object formats
and Git execution failures also stop publication. An optional raw-blob resolver now handles bounded regular-file content conflicts.
Claude enables only manifest/device metadata unions; transcript/file and canonical
staging conflict recovery remain required before activation. There is no interactive
mergetool or whole-side fallback in retained publication.

Private Git runs disable inherited Git overrides, global/system configuration,
system/global attributes, templates, hooks, replacement objects and automatic
maintenance. The shared runner itself allows only local file operations; network
operations belong to the explicit transport. Captured control-command output,
including merge-conflict diagnostics, has a 1 MiB limit. Overflow returns a
capacity error without exposing partial output for parsing. Strict integrity
checks suppress expected dangling-object/progress notices and stream unused
output to a discard writer; this does not skip damaged-object checks. The same
helper protects commit/seed bundle verification. Git neither checks out remote trees
nor runs configured clean/smudge filters. Merge-tree uses Git's built-in merge
behavior with this private configuration.

Before every push, raw blobs from the candidate are streamed through one
`git cat-file --batch` process into a fresh private directory. Each object ID,
type, size and delimiter must match the validated tree listing; truncated or extra
output fails. Input requests and output are streamed concurrently to avoid pipe
deadlocks. Cancellation/failure terminates and reaps this batch before returning.
Empty Git directories are materialized as well. This avoids export-ignore, filters, line-ending conversion and index
state. Regular files and executable modes are preserved. Links, submodules,
unsafe paths, case-colliding spellings and unsupported modes are refused.
Every path component follows the same policy on all hosts: valid UTF-8, at most
255 bytes, no Windows-reserved characters or ASCII controls, no trailing dot/space,
and no DOS device/console name (including extensions and superscript COM/LPT
digits). This deliberately uses a conservative common policy rather than the
current worker OS or Windows version's pathname rules.
The tree has a configurable byte limit (default 32 GiB), a 64 MiB listing limit,
a separate 64 MiB metadata budget, and at most one million file entries. Directory
metadata stores only immediate component names, avoiding cumulative-prefix
expansion for deep paths. The budget charges names, file descriptors, slice
capacity and conservative per-node/map overhead before retention; exhausting any
limit refuses the tree before materialization. These are per-tree limits, not
total Git object, pack, workspace or store quotas.

Validate and Audit inspect this exact raw directory. They must be context-aware,
read-only policies that do not require a Git checkout. After the callbacks, the engine checks exact directory membership and streams
files through Go SHA-1/SHA-256 Git-blob hashing against the original object IDs.
This detects changed files and extra/missing entries without starting per-file
Git processes or rewriting objects. A policy cannot clean inspected files while
leaving unsafe original blobs to publish. Executable modes stay attached to the
original candidate independently of host filesystem permissions.
Claude uses `backupgit.ValidateTree` for this boundary, followed by the existing
`engine.CheckPublishContext` secret/transcript audit. The synchronous checkout
validator stays unchanged.

## Claude retained publication adapter

`Service.PublishArtifact` consumes an `ArtifactPublishRequest` containing the
committed batch and an explicit `ArtifactTransport`. It detaches request values,
checks the configured roots/staging/remote, producer identity, sealed event
membership, capture reference key and committed phase. The commit reference must
carry the exact artifact key derived from the same policy request used by
`CommitArtifact`: capture reference, policy version, message, author name/email
and sealed timestamp. A valid bundle for the same capture built with different
commit policy or identity is refused before opening it or calling transport.
The verified bundle must also name that exact capture. Missing or corrupt
artifacts fail without recapture or rebuilding.

The transport must report its immutable destination and branch. Both must exactly
match the bound Claude configuration and the native publication plan (`main`),
and an empty configured remote is refused. This is a contract for trusted injected
code, not validation of arbitrary transport implementations. There is no default
transport, credential discovery, worker-login read or network activation.

The adapter revalidates bindings under the canonical staging lease and holds it
through publication, confirmation and cleanup. A protected read of settled HEAD
selects only committed local history; staged and unstaged files are untouched.
An absent checkout or confirmed unborn branch contributes no local history.
Unfinished merges, cherry-picks, reverts, rebases and sequencers, unmerged index
entries, malformed Git state and other read failures stop the attempt. Inherited
Git environment cannot redirect HEAD inspection. Auto chunk mode remains pinned
by the sealed binding even after the live marker or staging checkout disappears.

The native tree validator delegates attribute evaluation to Git in a new empty
private repository outside the raw tree. It checks every regular file, including
ignored files, using the actual root/nested attribute and macro syntax. `text`,
`eol`, `filter`, `ident` and `working-tree-encoding` must all be explicitly unset.
Host global/system/info attributes cannot override this check. No checkout or
filter runs and no `.git` is added to the audited directory. A single streamed
`check-attr` process verifies bounded NUL-delimited response fields against each
requested path/attribute pair. Path input is limited to 64 MiB and one million
files; the publisher already bounds and validates tree metadata before invoking
this policy. Failure and cancellation clean up the process group/job and reap
the direct child before returning. The streamed reader stays open until the
runner finishes; helpers retaining stdout cannot prevent EOF.

Claude supplies its native snapshot label, fixed queued author identity, sealed
event timestamp and four push/confirmation attempts. The committed store's byte
limit also bounds materialized publication trees. A returned `Publication` is
only evidence for persisting the pushed phase; this adapter does not update queue
state. QueueAdapter now supplies worker phase wiring. Config-history, retention,
local-only completion and broader native merge recovery remain separate work. Composition can supply
`NewConfiguredGitTransport` to reuse existing Git/`gh` authentication; broad
credential discovery is deferred. Synchronous behavior is unchanged.

## Confirmation and retries

A fresh initial fetch may show the retained capture already reachable from the
remote tip. The engine validates and audits that observed tree and returns success
without another push. This closes the crash window after server acceptance and
before the queue's pushed marker, even if local inputs disappeared meanwhile.

Otherwise, it audits the merged candidate, attempts a push, and fetches again.
Only a fresh remote observation containing the retained capture, with successful
validation and audit of the observed tree, returns a Publication result. A Git
push success alone is insufficient. A lost push response can still succeed when
the follow-up fetch proves acceptance. A failed confirmation returns no successful
result; a later retry observes the remote again.

If another writer advances the remote without publishing this capture, the next
attempt rebuilds the merge against that observed tip. A rejected push never forces
history backward. Attempt exhaustion returns ErrUnconfirmed. Cancellation stops
without further effects or confirmation. Success confirms that the capture is in
published history; it does not promise that later commits have not edited/reverted
its files. The caller still persists the queue phase and exact acknowledgement.
No local-only success is represented by this API.

Private workspaces are removed on normal return. Process death can leave them
behind. All retained Git commands, including transport and streamed validation,
now have cancellation ownership. Parent-death recovery, ownership of future
external merge tools, native conflict resolution,
cleanup/quotas, exact manual-sync coverage and queue lifecycle/rollback remain
required before queued hooks can be enabled.

## Validation

Synthetic tests use real bare Git remotes in SHA-1 and SHA-256 formats. They cover
newer local and remote histories, canonical state isolation, raw binary bytes and
executable modes, inherited Git overrides, deterministic candidate reconstruction,
remote races, accepted pushes with lost responses, missing confirmation,
replay without duplicate pushes, conflicts/unrelated history, unsafe trees,
validation/audit failure or mutation, portable-path fixtures built directly in Git
(including invalid UTF-8 and Windows device names), mismatched refs/captures, shallow history,
metadata/byte/output capacity refusal (including a large real conflicted merge),
quiet integrity checks that still reject corrupt dangling objects, malformed
batch output, empty directories,
cancellation and missing artifacts. Trace-based tests assert that a many-file
audit uses only a tree-listing process and one blob batch, in both hash formats. No real vendor data or
network credentials are used.

Claude adapter tests additionally cover invalid bindings/provenance/phases,
wrong destination/branch, missing/corrupt/foreign committed artifacts (including
each policy/identity field changed for the same capture), auto-mode
recovery after live inputs disappear, staging ownership during transport,
byte-for-byte preservation of canonical index/config/worktree, newer local commits,
lost-response replay, SHA-256 publication, cancellation cleanup, and native
attribute/secret rejection on both local candidates and already-published remote
trees. The shared attribute validator tests macros, nested overrides for all five
attributes, ignored paths, inherited Git overrides and streamed responses larger
than the ordinary command-output cap.


## Bounded retained metadata recovery

The optional Resolve callback accepts an exact path and raw base/ours/theirs blobs.
It is used only for regular-file content or add/add conflicts with both sides
present and consistent modes. Delete/edit, directory/file, mode and unsupported
path conflicts remain blocked. A nil resolver keeps the previous fail-closed
behavior. Claude enables its resolver in PublishArtifact; already-blocked queue
batches still require explicit Unblock before execution.

The publisher reads Git's NUL-delimited conflict stage records from
[merge-tree](https://git-scm.com/docs/git-merge-tree), with messages and rename
inference disabled. Only a clean conflict exit with complete bounded output is
eligible for parsing; cancellation, output overflow and child cleanup failures
cannot masquerade as conflict records. The publisher limits a merge to 128
conflicted paths, 1 MiB per input/output blob and 16 MiB across conflict inputs and
outputs. Existing control-output and whole-tree limits still apply.

All accepted outputs are written as raw Git blobs and applied through a private
bare-repository index. No checkout, filter, external merge driver, mergetool or
canonical-state edit is needed. Every resulting merge commit has both original
parents and the same deterministic publication identity/time/message. The entire
candidate still passes native attribute validation and secret auditing before a
push; fresh remote confirmation and replay rules remain unchanged. A declined
path prevents publication even if earlier paths were resolvable.

Claude resolves only the root manifest and device registry. The same pure union
functions now serve both synchronous and retained resolution: manifests retain
both project/link maps with ours winning shared keys, and device registries take
the newest sync entry while preserving a known account if the newer entry has
none. The synchronous fallback/reporting behavior remains unchanged.

Retained resolution requires schema 1, valid UTF-8 JSON and only known fields.
Malformed/unknown versions, unknown fields, duplicate keys (including case aliases),
and excessive nesting are refused rather than silently discarded. Unsupported
metadata is not replaced by whichever snapshot is newer. This deliberately
conservative decoder applies only to the new retained resolver.

Tests cover SHA-1/SHA-256, raw CRLF bytes, deterministic replay and both parents,
declined resolutions, delete/edit refusal, malformed/unsafe stage records,
cancellation and blob bounds. Claude fixtures exercise metadata union through
actual publication, preserved device provenance, unchanged canonical files/index/
config, unknown-field refusal and secret rejection after resolution. Transcript
chunk indexes, append-text recovery, other machine state, rename-aware recovery,
canonical merge repair and operational unblock/status commands remain future work.
