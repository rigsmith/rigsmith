---
type: fix
"github.com/rigsmith/rigsmith"
---

A lockstep group's release plan now lists every member, not just the ones carrying a changeset — the bump was already moving all of them.

Packages that share a `Directory.Build.props` take their version from that one file, so writing a bump into it moves every member whether or not it has a changeset. Coordination only ever ran over the members that were already releasing, so the plan reported a subset of what the release did. In Avalite, one changeset on `Avalite.Core` printed a five-package plan and then moved all nine: `Avalite.Editing`, `Avalite.Icons`, `Avalite.IconTool` and `Avalite.Previews.Sdk` were versioned with no plan entry, no changelog, and would have been published at the new version.

Lockstep now coordinates *and* adds the non-releasing members, the way fixed groups already do. Ignored packages are still skipped: the shared file moves their number, but `ignore` means "do not release this". The guard drops from "fewer than two releasing" to "none releasing", because with a shared version file a single releasing member is enough to move the whole group.

Three tests cover it — the pull-in, the ignored-member exclusion, and the no-changesets case that must not manufacture a release. The first was verified to fail against the previous behaviour.
