---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

With commits as a versioning source, housekeeping commits no longer release anything, as @unjs/changelogen skips them: a non-breaking `chore(deps)` or `chore(release)`, and a release commit itself (`chore: release`, `chore: release 1.2.0`, `chore: release core@1.2.0, ui@0.5.0`, release-please's `chore(main): release 1.2.0`). A release commit touches every package it versioned, so wherever the last release's baseline didn't cover it (before the release was tagged, a first release, a missing tag), it showed up as a patch of all of them. A breaking `chore(deps)!:` still releases.
