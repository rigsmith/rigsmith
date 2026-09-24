---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

`status --output` gives each release a `group`: the packages that have to be versioned together (one changeset naming both, a dependency, a fixed or linked group, a shared version file). `version --only <package>` versions just the named groups and leaves the rest for a later run, and unlike `--ignore` it works alongside `ignore` in the config, so a release can go out a group at a time.
