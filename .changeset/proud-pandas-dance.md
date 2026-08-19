---
type: fix
"github.com/rigsmith/rigsmith"
---

clauderig sync no longer aborts on a project memory/ symlink, and restore now recreates it: worktree slugs share memory with their main project via a symlinked directory, which the allowlist walker surfaced as a regular file — the copy then read a directory and the whole sync failed ("read …/memory: is a directory"). The walker now reports directory symlinks whose endpoints are both in the synced set, sync records them in the manifest (`links`), and restore recreates them — endpoints rewritten through the machine's slug map — whenever the target directory exists and nothing already occupies the link path. The shared memory itself still syncs once, under its canonical project slug.