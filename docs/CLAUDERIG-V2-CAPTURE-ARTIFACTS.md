# V2 sealed capture artifacts

This is the capture/sealing portion of milestone 6b.2. The shared execution driver
and Claude Capture/Publish split merged in #310. `Service.CaptureArtifact` now
prepares a durable, immutable input for commit/publication.
[Retained commits](CLAUDERIG-V2-RETAINED-COMMITS.md) now implements the next
commit/sealing step. Capture itself does not commit or push, expose a worker
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
canonical merge completion merged in #344. Unresolved canonical recovery and
repair before fresh capture remain pending.

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
seed or copying canonical staging. The guard refuses merges, standalone
MERGE_AUTOSTASH residue, active bisects, cherry-picks, reverts, rebases, sequencers and unmerged
index entries. It does not repair these states. Only a settled checkout is copied,
excluding Git metadata. It records HEAD and a durable seed-bundle
reference in the archive header. Seed retention must succeed before capture can
be acknowledged. The canonical checkout, index and refs are not changed.

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

Lock order is worker ownership (when used by the driver), artifact-store ownership,
canonical staging ownership, seed-store ownership while retaining ancestry, then
private capture ownership. Original contexts
are used for independent stores; derived contexts are only borrowed by operations
on the same store. The future concrete adapter must follow this order rather than
holding canonical staging ownership before calling CaptureArtifact.

## Limits and remaining work

One archive defaults to a 32 GiB limit. Claude also bounds the combined bytes it
copies from staging and sources by that limit, so workspace admission can be more
conservative than final archive size. This is not a total-store quota. Archives
are retained indefinitely for now. No automatic compaction, expiry, startup
cleanup, or migration is enabled. A process killed during a build can leave a
private temporary workspace containing raw inputs; startup cleanup under the
ownership locks is a rollout gate. Successful and ordinarily failed builds attempt to clean
up their own workspace. Unknown versions and corrupted captures fail closed.

The [commit adapter](CLAUDERIG-V2-RETAINED-COMMITS.md) now seals retained Git
bundles, and captures retain seeds before acknowledgement. Queue execution now
connects capture, commit and confirmed publication, with owned child cleanup.
Unresolved canonical recovery, repair before fresh capture, exact manual-sync
coverage and worker lifecycle remain activation gates. Local-only
completion, artifact/receipt cleanup, status and capacity remedies also remain
rollout gates. A mutable extracted working copy or recorded seed SHA alone does
not satisfy those requirements.

Validation uses synthetic sources: byte/mtime/chunk round trips, immutable reuse,
metadata, corruption, traversal/link refusal, build failure/cancellation/capacity,
source deletion, secret refusal/scrubbing, retention protection, seed dependency
persistence before capture acknowledgement, scoped attribution,
binding changes and unchanged canonical staging. The unchanged six-scenario Claude
compatibility baseline continues to guard existing sync behavior.
