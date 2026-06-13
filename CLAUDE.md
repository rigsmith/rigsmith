<!-- BEGIN clauderig:worktree-discipline -->
<!-- Managed by clauderig. Run `clauderig guide` to update; edits inside this block are overwritten. -->
## Worktree & PR discipline (enforced by `clauderig guard`)

A PreToolUse hook guards this environment. Work *with* it:

- **Never use the EnterWorktree/ExitWorktree tools, and never `cd` out of the
  repo root in a Bash command.** Both move this session's working directory, and
  Claude Code keys chat history to that folder path — moving it scrambles the
  conversation. They are denied. To act elsewhere, use an absolute path,
  `git -C <dir> …`, or a subshell `(cd <dir> && …)` (which doesn't move this shell).
- **Don't write code on `main`/`master`.** Make a branch + worktree first:
  run `clauderig worktree new <branch>`. It creates a sibling checkout at
  `<repo>-worktrees/<branch>` and opens it in a *new* VS Code window for review —
  this window stays put. Edit files in the worktree by absolute path, run git via
  `git -C <worktree> …`, then push and open a PR.
- **Docs and root config may go on the base branch directly** — `*.md`, the
  `docs/` and `.github/` trees, and top-level config (`*.toml`, `*.yml`, `*.json`,
  `LICENSE`, `.gitignore`). Everything else counts as code and needs a PR.
- **Override**, only when you must change code on the base branch:
  `export CLAUDERIG_ALLOW_MAIN=1` (this session) or `touch .claude/allow-main` (this repo).

Keep one VS Code window pinned to the primary repo as the continuous chat; treat
worktree windows as review/diff only.
<!-- END clauderig:worktree-discipline -->
