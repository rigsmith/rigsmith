---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

Generated packages are published, and only published: they are no longer git-tagged, and they publish at the access their own manifest declares.

Review caught both on the `publishDirs` work before it shipped. Packages named by `publishDirs` were joining the workspace's own package list, so the tagging phase would have pushed a ref per wrapper — 41 of them, on every release. And they inherited the workspace-wide `access`, which for a repo configured "restricted" would have published scoped public packages privately, or failed outright. Each generated manifest now carries `publishConfig.access` and that wins for it.

`publishDirs` globs must also stay inside the repository: `../elsewhere/*`, an absolute path, or a symlink resolving outside it are refused, rather than becoming a directory the publisher runs npm in.