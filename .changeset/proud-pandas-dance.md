---
type: fix
"github.com/rigsmith/rigsmith"
---

clauderig sync no longer aborts on a project memory/ symlink: worktree slugs share memory with their main project via a symlinked directory, which the allowlist walker surfaced as a regular file — the copy then read a directory and the whole sync failed ("read …/memory: is a directory"). Symlinks that resolve to directories are now dropped from the walk; the shared memory still syncs under its canonical project slug, so no content is lost.