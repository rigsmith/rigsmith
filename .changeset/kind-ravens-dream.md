---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

The release workflow's dry run builds under the last CLI version again, rather than under the window's tag.

GoReleaser takes its version from `git describe` when there is no tag to build from, and since the claudeRig UI started shipping on `ui/vX.Y.Z`, the newest tag in this history is usually the window's. That version is not merely the wrong number — it contains a slash, and the slash goes everywhere the version does: archives came out as `dist/changerig_ui/…`, a directory nobody asked for, and the casks as `version "ui/v0.3.0-SNAPSHOT-…"` pointing at `…/download/ui%2Fv0.3.0/…`.

Tag pushes were never affected: the ref is the version. But the dry run is the one run whose whole purpose is to prove packaging works before a real release, and it had quietly stopped proving it. It now names the last `v*` tag explicitly, which passes over the window's tags without having to know anything about them.