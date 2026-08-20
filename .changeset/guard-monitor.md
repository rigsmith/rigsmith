---
"github.com/rigsmith/rigsmith": patch
---

clauderig: the worktree/base-branch guard now covers Claude Code's new `Monitor` tool, which runs shell commands like `Bash` and was slipping past unchecked. Installing hooks also brings an already-installed clauderig hook up to date instead of leaving it on an older release's tool list — run `clauderig doctor --fix` (or `clauderig project install`) to update an existing repo, which `doctor` now flags.
