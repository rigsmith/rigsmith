# V2 sealed capture artifacts

This is the capture/sealing portion of milestone 6b.2. The shared execution driver
and Claude Capture/Publish split merged in #310. `Service.CaptureArtifact` now
prepares a durable, immutable input for commit/publication.
[Retained commits](CLAUDERIG-V2-RETAINED-COMMITS.md) now implements the next
commit/sealing step. Capture can finish an audited, already-staged canonical merge
before retaining its seed. It does not push, expose a worker
command, acknowledge a queue batch, or enable hooks. Installed sync behavior and
backup formats stay unchanged. The initial internal capture foundation had no
end-user changeset; subsequent v2 queue behavior has release changesets while
worker commands and queued hooks remain disabled.

## Shared archive and durability layers

`internal/agentrig/artifact` stores a complete archive under a SHA-256 key derived
from the binding and sealed event membership. The reference contains both that
key and the checksum of the archive. Its versioned header includes a bounded seed
reference; Claude records canonical staging's current HEAD when present. Before
the capture is sealed, its complete seed ancestry is retained in a private
bundle and its immutable artifact reference is recorded in `SeedReference`. See
[retained commits](CLAUDERIG-V2-RETAINED-COMMITS.md) for the dependency contract.
Retained publication and native conflict recovery are implemented; staged
canonical merge completion merged in #344, with refusal guard fixes in #345.
Fresh capture now invokes that completion before retaining ancestry. Unresolved
canonical conflict recovery remains pending.

A build runs in a private workspace, then streams regular files and directories
into one archive. Source symlinks, devices and Git metadata are not allowed in the
sealed output. Directory aliases from Claude's source walk are represented by
its normal native manifest after capture; they are not archive symlinks. The
archive preserves file bytes, modification times and the owner's executable bit.
ExtractWithMetadata additionally returns the verified header and archived modes
without another checksum pass. These modes are independent of host chmod support.
Extract verifies the reference before creating a new destination and refuses to
overwrite an existing tree. Unsafe paths and link entries are rejected. A failed
extract removes only the directory that operation created. Extraction creates a
working copy, not a second durable artifact or an automatic restore to live data.

A store-wide OS lock serializes builds. An existing valid key is verified and
rewritten through the durability layer before success, without invoking the
builder or rereading live sources. This confirms durability after an uncertain
prior publication. Corruption blocks reuse; it never triggers replacement with a
new capture of that key. Read-only Verify/Metadata calls do not confirm an
uncertain Build. Callers holding a saved reference must verify it, not use a
missing archive as permission to recapture changing sources.

`internal/agentrig/durable` now owns the existing queue file-flush and replacement
mechanics. Queue state and archives use the same implementation: flushed temporary
sibling, replacement without remove/copy fallback, and directory-chain flushes on
Unix. macOS retains full file flushing; Windows retains write-through replacement.
The existing queue uncertainty/recovery tests still cover those mechanics. This
is a request to a stable local filesystem to persist data, not a universal
hardware power-loss guarantee. Network/shared-host stores and concurrent external
changes to store directories are unsupported.

## Claude inputs and source attribution

CaptureBinding hashes canonical staging/source paths, remote, configuration,
machine, explicit profile set, resolved transcript storage mode, and the queued
capture policy version. It contains no plaintext remote credentials or config.
Producers save it; callers must supply freshly resolved inputs when executing.
Configuration changes reject old work rather than redirect it. Caller configuration
and machine maps are copied before the build. Path aliases that resolve differently
can conservatively reject a binding; inputs and directory ancestors must stay
stable during an operation.

Every event currently must name a CLI transcript found in the configured CLI
source. Missing or ambiguous sessions, unavailable selected flush sources, invalid
provenance and changed bindings fail. Desktop roots and explicitly named profiles
can be included in the full capture, but Desktop-only event routing is not yet
implemented. No live profile discovery occurs in this path.

CaptureProvenance identifies the producer's explicit source identity, including an
empty/unknown value. The worker's login is never read. Account inference applies
only to the CLI session IDs named by that batch; unrelated sessions discovered in
the walk do not inherit the event's account. Existing stronger attribution is
preserved, and a known source identity that conflicts with a requested session's
resulting ledger entry is refused. A delayed capture does not refresh the current
device registry with an old event identity; it retains the seeded registry.

## Capture sequence and isolation

The artifact builder takes staging ownership with the original context, verifies
the binding again, and calls the shared `SettledHead` guard before retaining a
seed or copying canonical staging. An already-staged two-parent merge can be
completed with `FinishStagedMerge`: validate and secret-scan both parent tips and
the exact staged tree before recording the merge and forgetting its metadata.
Supported unresolved conflicts use [sealed merge recovery](CLAUDERIG-V2-MERGE-RECOVERY.md).
Unsupported conflicts, standalone MERGE_AUTOSTASH residue, active bisects,
cherry-picks, reverts, rebases and sequencers remain blocked. Only a settled checkout is copied,
excluding Git metadata. It records HEAD and a durable seed-bundle
reference in the archive header. Seed retention must succeed before capture can
be acknowledged. Completion preserves the canonical index and worktree bytes;
only the audited merge commit and completed-operation metadata change. A later
seed-retention or capture failure does not undo that merge. Retry retains the
completed HEAD without creating another merge. Reusing an already-sealed capture
skips this work and does not repair a newer canonical operation.

This exclusion is cooperative: staging writers must honor the same lease.
The guard and copy are not an atomic snapshot against arbitrary external Git
commands or filesystem writes that bypass it. Rechecking HEAD before sealing
would not provide that guarantee either.

Allowed source files are copied into private frozen roots before Claude's engine
runs. These temporary inputs may contain raw transcript/config credentials, so
the containing workspace uses private permissions and stays outside every source
and backup root. The normal scrubber and full publication scan run on the output
before it can be sealed. Source-copy failures or concurrent size/mtime changes
abort; this is a collection of bounded file snapshots, not a filesystem-wide
transaction. A source removed after freezing cannot silently turn into a missing
capture. File symlinks are refused; creating supported directory aliases in the
private source snapshot requires the OS to permit symlink creation.
Alias target directories are retained even when empty or containing only excluded
files, so the native manifest preserves the alias without copying excluded data.

Requested transcripts' seeded copies are removed from the private output before
capture. They must be readable in the resulting output and have ledger entries;
an old staged copy cannot stand in for a missing or oversized requested source.
Queued capture disables age pruning and large-file throttling for this frozen
snapshot. The configured maximum-file policy and secret checks still apply.
Retention/space cleanup moves to a later publication/lifecycle policy; synchronous
capture keeps its existing retention and throttle behavior.

Claude artifact services acquire worker ownership when run by the queue, then
canonical staging ownership, then private artifact-store ownership. Capture can
acquire the seed-substore lease while retaining ancestry. Independent operation
contexts acquire separate stores; a derived store context is borrowed only for
sequential work on that same store. Staging ownership is attached separately to
command supervision and protects external writers after worker death.

## Limits and remaining work

One archive defaults to a 32 GiB limit. Claude also bounds the combined bytes it
copies from staging and sources by that limit, so workspace admission can be more
conservative than final archive size. Optional aggregate admission and interrupted
archive-write cleanup are described below. Archives remain retained indefinitely;
there is no automatic expiry, compaction, cleanup, CLI or hook wiring. A process
killed during a build can leave a private workspace containing raw inputs. Such
workspaces require reference and external-writer checks before reclamation.
Successful and ordinarily failed builds attempt to remove their own workspace.
Unknown versions and corrupted captures fail closed.

The [commit adapter](CLAUDERIG-V2-RETAINED-COMMITS.md) now seals retained Git
bundles, and captures retain seeds before acknowledgement. Queue execution now
connects capture, commit and confirmed publication, with owned child cleanup.
Unresolved canonical recovery, exact manual-sync
coverage and worker lifecycle remain activation gates. Local-only
completion, artifact/receipt cleanup, status and capacity remedies also remain
rollout gates. A mutable extracted working copy or recorded seed SHA alone does
not satisfy those requirements.

Validation uses synthetic sources: byte/mtime/chunk round trips, immutable reuse,
metadata, corruption, traversal/link refusal, build failure/cancellation/capacity,
source deletion, secret refusal/scrubbing, retention protection, seed dependency
persistence before capture acknowledgement, scoped attribution,
binding changes, audited merge completion before capture, seed survival after
canonical history disappears, source-failure retry, and queue blocking/offline
replay. Index and worktree bytes remain unchanged. The unchanged six-scenario Claude
compatibility baseline continues to guard existing sync behavior.

## Store capacity and interrupted archive writes (6b.7b.1)

`artifact.Store.MaxStoredBytes` optionally limits the logical bytes of direct
sealed `.capture` files in that store. Zero preserves unlimited aggregate
admission; the separate 32 GiB default per-archive limit still applies. A negative
aggregate limit is invalid. Every cooperating writer of a store must use the same
configured limit. This remains an internal policy field, with no user config key.

The quota is runtime admission policy and is deliberately excluded from the
queued binding and artifact key. A resolver may supply a new quota on the next
attempt: raising it repairs capacity exhaustion for the same queued identity;
lowering it can block a new archive but cannot change captured bytes or force
recapture of retained output. Content-selection and destination policies remain
bound as before. Writers must coordinate policy changes between operations.

New builds inventory existing archive sizes while holding the artifact-store
lease, then bound the archive stream to the smaller of the per-archive limit and
remaining sealed capacity, including archive framing and checksum. No header
space means rejection before invoking the builder. Exceeding the aggregate bound returns
`artifact.ErrStoreFull` without publishing a partial archive; the Claude queue
classifies it as `capacity-exceeded` and blocks for explicit repair. Existing
verified archives still reflush and resume even if their aggregate limit was
lowered below current usage. Limits never authorize deletion or recapture.

The quota excludes build workspaces, in-progress archive files, publication
scratch and substores; it is not a disk-free-space reservation or a bound on peak
filesystem usage. Builders can use scratch before archive size is known. A seed
substore inherits the same numerical policy as its capture store, but has its own
independent quota. Commit stores use their explicitly supplied policy.

`Store.Capacity` observes direct archive bytes/counts, interrupted archive-write
bytes/counts, build-workspace count, other entries and configured sealed headroom.
It does not hash files, traverse directories, follow links, or report recursive
storage/free space. Corrupt and unrecognized regular `.capture` files still count
against admission. Nonregular archive/write candidates, inaccessible entries,
size overflow and directories over 100,000 entries refuse the inventory; no
partial inventory authorizes a build or deletion. The private canonical store
directory and its ancestors must remain stable, as required by Build.

`Store.CleanupInterruptedWrites` acquires artifact ownership without waiting and
removes only direct regular files in the reserved `.durable-*` archive-write
namespace. The prefix must have a nonempty suffix; `.capture` takes precedence
and is retained. This namespace is exclusively disposable scratch: callers must
never place unrelated data there. Cleanup uses names, not creator provenance, so
even a file named `.durable-user-data` is eligible for deletion. This contract
applies only inside the private artifact store; nested namespaces are not scanned.
It validates the whole inventory before deleting anything and checks
file identity again before each removal. Live owners and persistent store fences
block cleanup. Missing stores remain missing. Failure or cancellation during
removal returns partial counts; retry safely handles what remains. This is space
reclamation, not a durable acknowledgement: deleted scratch can reappear after
power loss and require another cleanup.

Sealed archives, `.capture-work-*` directories, seed/recovery substores,
publication scratch and entries outside the reserved namespace are never deleted. Build workspaces may
still have external Git writers protected by staging ownership after the parent
exits; an artifact lease alone does not prove those writers stopped. Writer-owned workspace cleanup is described below. Reference-aware sealed-artifact
cleanup remains in 6b.7b.2b before automatic worker/hook integration.

Synthetic native tests cover admission, concurrent builders, retained reuse over
a lowered quota, corruption accounting, cancellation/partial removal, store
fences and process death during a real durable rewrite. The process test verifies
that a live writer blocks cleanup and that its death leaves the sealed archive
and build workspace intact. Symlink-refusal cases skip on platforms where creating
symlinks is unavailable.

## Writer-owned workspace cleanup (6b.7b.2a)

`commitartifact.CleanupWorkspaces` is an explicit internal maintenance operation.
It takes a staging directory and its exclusive private capture/commit stores.
The Claude `Service.CleanupArtifactWorkspaces` adapter validates the current
binding and keeps those stores outside native source and staging roots before
calling the shared operation. It rechecks the binding after all writer leases
are acquired, so a staging-backed auto-chunking change in the acquisition gap
refuses deletion. Shared callers may supply a read-only `Validate` callback,
which receives the active staging-lease context under all writer locks.
No command, hook or startup path invokes cleanup.

Private-store paths must come from trusted local resolution or explicit operator
selection. The capture binding describes content and destination policy; it does
not certify the ownership of caller-supplied scratch paths. This internal API is
not a boundary for accepting arbitrary paths from queue payloads or remote data.
Rollout callers must preserve the existing trusted-resolver boundary and exclusive
association between private stores and staging.

Cleanup acquires staging, capture, seed and commit leases without waiting and
holds all of them through removal. This excludes both archive builders and
external Git writers that retain staging ownership after their parent exits.
Every participating external writer must hold the same staging lease until
verified child cleanup, using supervision and persistent restart fencing for
owner death. An unresolved fence refuses maintenance; cleanup never clears one.
Older or uncoordinated writers and stores shared by multiple staging directories
are outside this contract. Callers pass an independent context, not a borrowed
lease, and retain stable private roots and ancestors throughout the operation.

Only these direct directory namespaces are disposable:

| Store | Reserved workspace prefixes |
| --- | --- |
| Capture store | `.capture-work-` |
| Capture `seeds` substore | `.capture-work-` |
| Commit store | `.capture-work-`, `.publication-`, `.startup-history-`, `.confirmation-` |

Each prefix requires a nonempty suffix. These names are reserved for disposable
scratch, not unrelated user data; eligibility comes from the namespace and
writer-ownership contract, not creator provenance. All other entries, sealed
archives, `.durable-*` files and recovery substores remain untouched. In
particular, cleanup does not traverse merge intents or scan the OS temporary
directory for relocated merge workspaces: the private stores' leases do not
identify ownership of those external paths. Those paths remain retained.
`SyncWithCoverage` also places `.confirmation-*` workspaces under the queue
directory, not the commit store. This API never scans that queue parent; adding
queue-worker ownership to reclaim those confirmations remains in 6b.7b.2b before
hook rollout.

Existing root aliases are canonicalized, overlaps are rejected, and the `seeds`
substore cannot be a link. Missing artifact stores are skipped without creation;
missing staging refuses cleanup. Inventory reads at most 100,000 direct entries
per store in batches of 128. Every candidate in every store must be a real
directory before any deletion starts. A candidate link, regular file, inaccessible
entry, excessive inventory, busy owner or fence refuses without deleting earlier
candidates. The opened filesystem root must match the directory observed before lock
acquisition, rejecting a leaf or ancestor replacement across that gap. Stable
roots remain a caller precondition; this is not a sandbox against hostile local
path mutation. Removal uses that pinned root and rechecks candidate identity;
nested links are removed without following their targets.

Results count fully removed top-level workspaces in this attempt. A failure can
partially empty its current workspace without increasing the count. Cancellation
is checked between directories; an in-progress recursive removal may finish
first. Retry handles remaining scratch. This is space reclamation, not a durable
acknowledgement; power loss can resurrect deleted entries. It neither changes
queue state nor authorizes deletion or rebuilding of sealed output. Reclaiming
sealed captures, seeds and commits still needs reference checks in 6b.7b.2b.

Synthetic tests cover every reserved namespace, retained/recovery bytes, all
writer locks and fences, invalid late candidates, linked roots/candidates/nested
entries, read-only object files, partial failure/cancellation and retry. A real
subprocess holding only staging ownership blocks cleanup until it dies; its
abandoned workspace is then removed while the sealed archive survives. This
models an independently owned writer, not an actual OS reboot. Claude source-fixture integration
tests require `CLAUDERIG_E2E=1`; symlink cases skip if the host cannot create links.
A deterministic adapter test changes the auto-chunking marker under a cooperating
staging lease between request preparation and cleanup, and verifies refusal with
all scratch retained. Shared tests prove the validator holds all writer leases and
reject leaf/ancestor replacement before opening a workspace root.
Native CI runs these checks alongside the unchanged pinned v1 compatibility suite.
