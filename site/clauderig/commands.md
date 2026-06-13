# Commands

| Command | What |
|---|---|
| `init` | First-run wizard: remote (private), machine identity, roots, hooks |
| `sync` | Walk → redact → manifest → tripwire → commit → push (`--dry-run`) |
| `pull` | Fetch latest into the staging repo (no write to `~/.claude`) |
| `restore` | Restore here, rewriting paths (`--dir`, `--backup`, `--force`, `--prune`) |
| `status` | Sync state: remote, last sync, roots, hooks |
| `global` | `install` / `uninstall` / `status` the global sync hooks in `~/.claude` (alias `hooks`) |
| `project` | `install` / `uninstall` / `status` this repo's guard hook + CLAUDE.md guide (committed) |
| `local` | same as `project`, but gitignored (`.claude/settings.local.json`) |
| `guard` | PreToolUse hook enforcing worktree/PR discipline (wired by `project`/`local`) |
| `worktree` | `new` / `list` / `open` / `rm` / `prune` sibling worktrees in their own review window (alias `wt`) |
| `branch` | `list` / `rm` / `prune` local branches; prune reaps merged (or, with `--gone`, gone-upstream) ones; alias `br` |
| `prune` | One sweep: reap merged/done worktrees, then their branches and other merged (`--gone`) branches; alias `tidy` |
| `guide` | `install` / `uninstall` / `status` / `show` the CLAUDE.md block standalone |
| `config` | `get` / `set` / `show` / `path` / `edit` |
| `doctor` | Health-check environment + sync + worktree discipline (`--fix` repairs) |
| `ui` | Interactive dashboard |

## The sync → restore loop

`sync` snapshots your `~/.claude` setup, redacts secret-bearing fields, rewrites
machine-specific paths into a portable form, and commits/pushes to your private
repo. `restore` does the inverse on another machine: it pulls, rewrites the
portable paths into this OS's slugs, and merges — keeping any local secrets in
place so a new machine simply re-authenticates.

## Hooks

```sh
clauderig hooks install
```

Wires two Claude Code hooks: **SessionStart → pull** (so each session starts
from the latest synced state) and **Stop → sync** (so your work is captured when
a session ends). Both are portable across OSes and idempotent.

## Worktree discipline

`clauderig guard` (a PreToolUse hook) and `clauderig worktree` make worktrees and
PRs the default path for Claude Code, and stop a session from scrambling your VS
Code chat history by moving its working directory. Chat history is keyed to the
folder path, so the model edits from one pinned window while worktrees open in
their *own* window for review only.

```sh
clauderig worktree new <branch>   # sibling checkout off mainline + a new review window
clauderig worktree new fix/x --base release-1 --no-open
clauderig worktree list           # this repo's worktrees (alias: ls)
clauderig worktree open <branch>  # (re)open a worktree's review window (branch or path)
clauderig worktree rm <branch>    # remove the worktree, keep the branch (-f if dirty)
```

Worktrees live at `<parent>/<repo>-worktrees/<branch>` — a **sibling** of the
repo, so they never clutter the primary checkout and each gets its own
review-window history. `new` never moves the session's cwd; it prints the path
and opens a separate window.

### Configuring the review window

By default `new` opens a new VS Code window (`code -n <path>`). Both *whether* it
opens and *what* it opens are configurable:

```sh
clauderig config set worktree.autoOpen false      # never auto-open (like --no-open)
clauderig config set worktree.openCmd "cursor -n"  # open Cursor instead of VS Code
clauderig config set worktree.openCmd ""           # reset to the default (code -n)
```

This writes a `worktree` block to `~/.clauderig/config.json`:

```json
"worktree": { "autoOpen": false, "openCmd": "cursor -n" }
```

- **`autoOpen`** (default `true`) — whether `new` opens a window at all.
  `worktree open` is an explicit request and always opens regardless.
- **`openCmd`** (default `code -n`) — the program plus any flags; the worktree
  path is appended as the final argument and run directly (no shell). Examples:
  `code -n`, `cursor -n`, `code-insiders -n`, `subl -n`, `idea`.

When the opener isn't on `PATH`, `new`/`open` print the command to run instead,
and `clauderig doctor` flags it.

::: tip
See the [worktree-discipline doc](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/WORKTREE-DISCIPLINE.md)
for the guard rules and the full model.
:::

::: tip
See the [design doc](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/CLAUDERIG-DESIGN.md)
and [roadmap](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/CLAUDERIG-ROADMAP.md)
for the full picture.
:::
