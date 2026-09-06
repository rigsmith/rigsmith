# V2 retained capture commits

Milestone 6b.2 now has a commit/sealing path following the capture artifacts in
[#311](https://github.com/rigsmith/rigsmith/pull/311). The shared
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
checks. The caller owns the extracted destination on success; a failed open
removes only its own destination. Open is read-only with respect to the durable
store and does not confirm an uncertain Build.

## Snapshot and ancestry isolation

The writer extracts the capture into a private temporary workspace, runs the
required vendor preparation and audit, and writes raw blobs and trees into a new
private bare repository. It bypasses filters, ignore rules, line-ending and
encoding conversions. File bytes and executable modes come from the sealed
snapshot. Git environment overrides, global/system configuration, templates,
replace objects and hooks do not influence the private writer. This avoids
copying a user's index or Git configuration into queued work.

For a seeded capture, the parent is exactly the SHA in the capture header. The
writer imports that commit and its ancestors from the canonical repository under
staging ownership, independently of its current HEAD. A seedless capture creates
a root commit. Newer canonical commits, staged edits and unstaged files are never
swept into the snapshot. The writer does not move canonical refs or write its
index or checkout. No network protocol is enabled for this operation.

The final bundle has no external prerequisites. After it is sealed, deleting or
garbage-collecting the original repository cannot remove its retained commit or
ancestry. Before the first successful build, however, the capture's recorded seed
must still exist. Missing seed objects fail closed. Protecting that interval with
capture-time seed retention remains a required integration step; a SHA in the
capture archive alone is not sufficient.

Claude revalidates the canonical root/store/remote/configuration binding and every
event's provenance, requires the captured phase, and checks that CaptureRef's key
matches the sealed event membership. Missing live transcripts after capture do
not trigger recapture. Commit and capture stores must be disjoint and outside all
source and staging roots. The lock graph is capture store → canonical staging →
commit store; reading an immutable capture does not acquire its writer lock.
Queue operations continue to use the original cancellation context.

## Remaining integration gates

- Retain seed objects from capture time until the first commit bundle is sealed.
- Implement Push using the retained commit, explicit remote confirmation,
  conflict recovery, and an audited merge with newer synchronous/remote history.
  This layer intentionally does not install its snapshot over newer work.
- Validate native Git attributes and audit the materialized publication tree at
  the publication boundary, as synchronous Publish already does.
- Own Git and merge-tool process trees across cancellation and parent death.
  Command cancellation and a bounded pipe wait do not establish that ownership.
- Add exact manual-sync event coverage, local-only completion policy, queue/status
  commands, rollback/draining, artifact/receipt cleanup and capacity remedies.

Bundles currently retain complete seed ancestry, so history can be duplicated
across artifacts. The artifact limit bounds each final archive; temporary Git
objects and packs are not yet governed by a total workspace/store quota. Normal
success/failure attempts to remove private workspaces, but process death can leave
them behind. No expiry or cleanup worker is enabled. These storage and process
lifecycle gates must be resolved before enabling high-volume hooks.

Synthetic tests cover byte preservation despite Git environment/attribute/ignore
settings, deterministic Git identity, immutable retries without original inputs,
self-contained ancestry, canonical HEAD/index isolation, missing seeds, mismatched
bindings/provenance/phases, auditing and preparation failures, corruption, malformed
bundles, cancellation, links and final artifact capacity. The fixed six-scenario
Claude compatibility baseline remains unchanged.
