---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

New option `"versioning": { "record": true }` keeps a record of every release in `.changeset/versions.json`, so:
- `doctor` catches a version edited by hand, and a release that was never tagged (for a package that's been tagged before, so not a first release);
- with commit-based versioning, each package counts new commits from its last recorded release, even if its tag is missing.

It's off by default.
