# V2 retained publication

Milestone 6b.2 now has an internal shared publication engine in
`internal/agentrig/commitartifact.Publish`. It consumes the durable commit bundles
from #315/#317, merges committed history in a private repository, and confirms
remote ancestry before returning success. This is a library boundary with real
local-Git transport tests. Claude's concrete queue Push adapter, native policy
wiring, production transport and process ownership are still next steps. No
command or hook calls this engine, and synchronous publication is unchanged.
There is no end-user changeset for this internal step.

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
Transport implementations are trusted code, not a sandbox: production wiring must
honor these requirements and own its child processes.

## Merging and byte checks

The engine merges the retained capture with the supplied local commit, then with
the freshly observed remote tip. Ancestor relationships take the appropriate
existing commit. Divergence uses Git's `merge-tree --write-tree`, followed by a
commit with explicit parents, identity, timestamp and message. Identical inputs
produce the same candidate after an interrupted attempt. Git must support that
merge-tree mode; failure is surfaced, with no fallback to a checkout merge.

Conflicts fail closed with ErrConflict. Unrelated history, invalid object formats
and Git execution failures also stop publication. There is no automatic choice of
one vendor's side, native conflict resolver or interactive mergetool yet. The
Claude adapter must add its native policy and recovery path before activation.

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
Claude's existing checkout-based attributes validator cannot simply be passed to
this API: a native validator for the materialized tree is a required integration
step, along with the existing secret/transcript audit.

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
behind, and a bounded command pipe wait is not ownership of Git/transport child
processes. Production transport, process ownership, native conflict resolution,
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
