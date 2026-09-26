---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

With commit-based versioning, housekeeping commits no longer trigger a release. That means `chore(deps)`, `chore(release)`, and release commits like `chore: release 1.2.0`. A breaking one still does.
