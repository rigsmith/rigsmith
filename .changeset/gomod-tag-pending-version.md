---
"github.com/rigsmith/rigsmith": patch
---

Fix the `tag` step never advancing a Go module past its first release. The gomod adapter treated the latest git tag as authoritative over the `// rigsmith:version` comment, so after `version` bumped the comment to the pending release, `shiprig tag` re-read the *previous* version from the existing tag and refused to create the new one ("0 tags, 1 already present"). It now takes the higher of the comment and the latest tag, so the comment — bumped ahead of the tag for a pending release — wins and the tag step creates `vX.Y.Z`. A released tag ahead of the comment is still authoritative.
