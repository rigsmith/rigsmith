---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

`"versioning": { "record": true }` keeps a release record in `.changeset/versions.json`, as release-please's manifest does: `version` writes the version every package it releases lands at under `released`, stamped or not (not for a snapshot or a range-only rewrite), and `doctor` flags a manifest that differs from the record (a hand edit) and a recorded release with no tag for a package that's been tagged before. The record is never a version source, and it's off by default: canon @changesets keeps no record.
