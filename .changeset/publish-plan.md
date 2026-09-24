---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`shiprig publish-plan` shows what a publish would release, as `changeset publish-plan` does: it asks each package's registry whether its version is already there, and lists packages released by their git tag alone (Go modules, desktop apps, private packages with `privatePackages.tag`) when the tag is missing. `--output <file>` writes @changesets v3's plan JSON in dependency order, for the split build/publish flow.
