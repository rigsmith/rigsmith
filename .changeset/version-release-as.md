---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

`version --release-as <package>=<version>` releases a package at an exact version without the interactive prompt, so CI can do it (a bare `--release-as <version>` works when one version is releasing). It's repeatable, shows in a `--changelog` or `--dry-run` preview, and needs the package to be releasing already. Without the flag nothing changes.
