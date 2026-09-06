# V2 sealed capture artifacts

This is the capture/sealing portion of milestone 6b.2. The shared execution driver
and Claude Capture/Publish split merged in #310. `Service.CaptureArtifact` now
prepares a durable, immutable input for a future commit/publication adapter.
It does not implement that adapter's Commit or Push methods, expose a worker
command, acknowledge a queue batch, or enable hooks. Installed sync behavior and
backup formats stay unchanged; no end-user changeset is needed for this internal
step.

## Shared archive and durability layers

`internal/agentrig/artifact` stores a complete archive under a SHA-256 key derived
from the binding and sealed event membership. The reference contains both that
key and the checksum of the archive. Its versioned header includes a bounded seed
reference; Claude records canonical staging's current HEAD when present. This
records the reference, but does not pin Git objects against later garbage
collection. The publication adapter still needs commit retention and merge
recovery.

A build runs in a private workspace, then streams regular files and directories
into one archive. Source symlinks, devices and Git metadata are not allowed in the
sealed output. Directory aliases from Claude's source walk are represented by
its normal native manifest after capture; they are not archive symlinks. The
archive preserves file bytes, modification times and the owner's executable bit.
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
the binding again, refuses an unsettled canonical merge, and copies the canonical
staging tree excluding Git metadata. It records HEAD in the archive header. The
canonical checkout, index and refs are not changed.

Allowed source files are copied into private frozen roots before Claude's engine
runs. These temporary inputs may contain raw transcript/config credentials, so
the containing workspace uses private permissions and stays outside every source
and backup root. The normal scrubber and full publication scan run on the output
before it can be sealed. Source-copy failures or concurrent size/mtime changes
abort; this is a collection of bounded file snapshots, not a filesystem-wide
transaction. A source removed after freezing cannot silently turn into a missing
capture. File symlinks are refused; creating supported directory aliases in the
private source snapshot requires the OS to permit symlink creation.

Requested transcripts' seeded copies are removed from the private output before
capture. They must be readable in the resulting output and have ledger entries;
an old staged copy cannot stand in for a missing or oversized requested source.
Queued capture disables age pruning and large-file throttling for this frozen
snapshot. The configured maximum-file policy and secret checks still apply.
Retention/space cleanup moves to a later publication/lifecycle policy; synchronous
capture keeps its existing retention and throttle behavior.

Lock order is worker ownership (when used by the driver), artifact-store ownership,
canonical staging ownership, then private capture ownership. Original contexts
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
ownership locks is a rollout gate. Successful and ordinarily failed builds clean
up their own workspace. Unknown versions and corrupted captures fail closed.

Next: integrate retained capture artifacts with Commit/Push, preserve and recover
Git commit references across offline publication, define merge/manual-sync
coverage, and own child processes before exposing queued execution. Local-only
completion, artifact/receipt cleanup, status and capacity remedies also remain
rollout gates. A mutable extracted working copy or recorded seed SHA alone does
not satisfy those requirements.

Validation uses synthetic sources: byte/mtime/chunk round trips, immutable reuse,
metadata, corruption, traversal/link refusal, build failure/cancellation/capacity,
source deletion, secret refusal/scrubbing, retention protection, scoped attribution,
binding changes and unchanged canonical staging. The unchanged six-scenario Claude
compatibility baseline continues to guard existing sync behavior.
