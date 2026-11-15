---
type: fix
"github.com/rigsmith/rigsmith"
---

`self-update` now updates the whole rigsmith family — rig, shiprig, clauderig, codexrig, changerig and brewrig — to the same release in one run, from whichever tool you run it in. Before, `rig self-update` moved only rig and left the other tools behind. It installs into the directory the running tool lives in, `--check` lists every tool's installed version against the latest release, and the run reports what moved, from which version to which. A Homebrew or npm install is pointed at its own package manager instead. Nothing is installed until the release checks out: `self-update` verifies the release's signed checksums and runs the release's own installer only if it matches them.