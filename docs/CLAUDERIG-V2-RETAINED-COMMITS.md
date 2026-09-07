# V2 retained capture commits

Milestone 6b.2 has capture artifacts from
[#311](https://github.com/rigsmith/rigsmith/pull/311) and retained commits from
[#315](https://github.com/rigsmith/rigsmith/pull/315). Capture-time seed retention
now closes the interval between those phases. The shared
`internal/agentrig/commitartifact` package converts a sealed capture into a Git
snapshot and retains its complete ancestry in a self-contained bundle. Claude's
`Service.CommitArtifact` supplies native audit, attributes, labels and queue
binding validation. This remains internal: no worker command, push adapter,
manual-sync acknowledgement or queued hook is enabled. Synchronous commands are
unchanged, so this step has no end-user changeset.

## Identity and durability

The input is an existing capture reference, never a mutable staging checkout.
The output is another shared artifact archive, in a separate private commit
store, containing `commit.bundle` and bounded `commit.json` metadata. The header
records the resulting commit SHA. Metadata binds that SHA, tree, parent and source
capture reference. The bundle advertises `refs/rig/capture` (exported as
`commitartifact.RefName`); a consumer imports that explicit ref. It is not a
normal main-branch clone or a remote destination.

The commit artifact key hashes a format/policy version, complete capture
reference, commit message, explicit author and enqueue timestamp. Git author and
committer identity are supplied by the adapter; worker Git configuration and the
current vendor login do not select them. Claude uses `clauderig@localhost`, its
native snapshot label and the last sealed event's enqueue time. The source account
remains in Claude's native ledger. Identical inputs produce the same Git commit
identity even if creation is interrupted before the queue stores its marker.

A successful Build durably seals the bundle with the existing artifact writer.
An identical retry verifies and reflushes that archive without loading capture
bytes, preparing/auditing a new tree, or consulting parent objects again. Errors
and uncertain durability return no usable reference. Retry the same request;
never advance the queue on a readable file alone. A corrupt output blocks reuse.
Once a committed reference has been recorded, a missing output must block work;
calling Build to replace it is not recovery.

`commitartifact.Open` checks the archive, strictly reads bounded metadata, extracts
into a new destination, verifies the bundle in an empty repository, imports its
explicit ref, compares commit/tree/parent metadata and runs Git's strict object
checks. Extraction returns verified header metadata with the files, avoiding an
additional full-archive checksum pass in both Build and Open. The caller owns the
extracted destination on success; a failed open
removes only its own destination. Open is read-only with respect to the durable
store and does not confirm an uncertain Build.

## Snapshot and ancestry isolation

The writer extracts the capture into a private temporary workspace, runs the
required vendor preparation and audit, and writes raw blobs and trees into a new
private bare repository. It bypasses filters, ignore rules, line-ending and
encoding conversions. File bytes and executable modes come from the sealed
snapshot. Archived file modes remain separate from host filesystem permissions,
so a Windows worker preserves executable bits from a Unix capture. Preparation
and audit receive the caller context; Claude checks cancellation during attributes
reads, between audit entries and during streaming transcript/index scans. Git
environment overrides, global/system configuration, templates,
replace objects and hooks do not influence the private writer. This avoids
copying a user's index or Git configuration into queued work. All retained Git
commands use the [owned process runner](CLAUDERIG-V2-GIT-TRANSPORT.md#command-ownership-and-cleanup),
which cleans up helpers before returning after normal completion or cancellation.

For a seeded capture, the parent is exactly the SHA in the capture header. Before
sealing that capture, Claude holds staging ownership and calls the shared
`RetainSeed` helper. It imports the commit and its complete ancestry into a private
repository, creates a bundle, verifies/imports it in an empty repository and runs
strict object checks. Only then does the artifact writer durably seal the seed.
The capture records the resulting immutable reference in `Metadata.SeedReference`.
A seedless capture has neither a parent SHA nor a seed reference.

Seeds live in the reserved `seeds/` substore beneath the private capture directory.
`SeedStore` derives this location and inherits the per-artifact size limit. Each
seed archive contains only `seed.bundle` and advertises `refs/rig/seed`; none of it
is extracted into the native backup tree or published as user content. The key
hashes the seed format version and exact commit SHA, so captures sharing one seed
reuse and reflush the same durable artifact. Retention never writes refs, index,
config or working files in canonical staging.

Commit creation imports the retained seed named by the capture, compares its
header and advertised commit with the captured parent, and checks the bundle in
an empty repository. It never fetches ancestry from the live canonical repository.
Missing, corrupt, mismatched or incomplete seed history blocks the operation;
there is no live-source fallback or root-commit substitution. The first commit can
therefore be created after canonical staging is deleted, replaced or garbage
collected. Newer canonical commits and staged edits remain untouched.

A capture cannot acknowledge an uncertain or failed seed write. Retrying seed
retention with the same SHA reflushes the existing archive, without requiring the
source repository. A process interrupted after seed persistence but before capture
persistence may leave an unreferenced seed; it is retained rather than risking
another capture's dependency. No automatic seed expiry is enabled. After the final
commit bundle is sealed it contains complete ancestry independently of both the
capture and seed stores.

The seed reference is a bounded, optional field in the archive metadata; older
readers reject the unfamiliar nonempty field rather than ignore it. Claude's
sealed-capture policy revision is now v2 so old queue bindings cannot silently
resume with the new dependency contract. Legacy seeded captures that contain only
a SHA are not automatically upgraded or recaptured. This code is still unactivated;
queue migration and rollback remain required before a production rollout.

Claude revalidates the canonical root/store/remote/configuration binding and every
event's provenance, requires the captured phase, and checks that CaptureRef's key
matches the sealed event membership. For auto chunking, it validates the resolved
mode already pinned by the binding digest instead of rereading the live storage
marker; deleting that marker or staging does not prevent reuse of a sealed bundle.
Configuration, path and provenance checks still apply. Missing live transcripts
after capture do not trigger recapture. Commit and capture stores must be disjoint and outside all
source and staging roots. The lock graph is capture store → canonical staging →
commit store, with staging → seed store during capture-time retention. Reading
immutable captures or seeds does not acquire their writer locks.
Queue operations continue to use the original cancellation context.

## Remaining integration gates

- The [shared retained publication engine](CLAUDERIG-V2-RETAINED-PUBLICATION.md)
  now merges newer committed histories and requires fresh remote confirmation.
  Claude's `Service.PublishArtifact` now checks batch/destination bindings and
  applies native Git attributes and secret auditing to the raw publication tree.
  [HTTPS/SSH/local transport](CLAUDERIG-V2-GIT-TRANSPORT.md) now supplies explicit
  credentials; all retained Git commands now have cancellation cleanup. SSH now
  requires explicit identity/host-trust files. Add authentication discovery and
  native conflict recovery next. The engine and adapter leave
  canonical staging untouched.
- Establish parent-death recovery and ownership for future external merge tools.
  Retained Git cancellation cleanup does not establish abrupt-death recovery.
- Add exact manual-sync event coverage, local-only completion policy, queue/status
  commands, rollback/draining, artifact/receipt cleanup and capacity remedies.

Seeds are deduplicated by exact commit SHA, but seed and final commit bundles
retain complete ancestry, so history can still be duplicated across artifacts. The artifact limit bounds each final archive; temporary Git
objects and packs are not yet governed by a total workspace/store quota. Normal
success/failure attempts to remove private workspaces, but process death can leave
them behind. No expiry or cleanup worker is enabled. These storage and process
lifecycle gates must be resolved before enabling high-volume hooks.

Synthetic tests cover byte preservation despite Git environment/attribute/ignore
settings, deterministic Git identity, immutable retries without original inputs,
self-contained ancestry (SHA-1 and SHA-256), first commit after repository deletion
or actual seed pruning, canonical HEAD/index isolation, missing seeds, mismatched
bindings/provenance/phases, auditing and preparation failures, corruption, malformed
bundles, shallow-history refusal, cancellation, links, seed capacity failure before
capture acknowledgement and final artifact capacity. The fixed six-scenario
Claude compatibility baseline remains unchanged.
