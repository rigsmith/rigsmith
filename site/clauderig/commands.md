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
| `guard` | The PreToolUse hook that enforces worktree/PR discipline — invoked by Claude Code, not run by hand (wired in by `project`/`local`) |
| `guide` | `install` / `uninstall` / `status` / `show` the CLAUDE.md guide block standalone (`--global` targets `~/.claude/CLAUDE.md`, `--path` overrides; `install` previews in a scrollable UI, skipped with `-y` or off a TTY) |
| `mcp` | `list` (alias `ls`) / `get` / `add` / `remove` (alias `rm`) / `enable` / `disable` MCP servers (`--scope user｜project｜local`, `--transport stdio｜http｜sse`, `--env`, `--header`); bare `mcp` on a TTY opens an interactive screen (mirrors `claude mcp`) |
| `account` | Manage multiple Claude Code logins: `add` / `list` (alias `ls`/`status`) / `run <id｜email> [-- claude args]` / `switch` / `sessions` (alias `ps`) / `remove` (alias `rm`) / `purge`. `run --no-share` isolates a session; `switch` takes `--dry-run` / `--force` / `--kill` |
| `config` | `get` / `set` / `show` / `path` / `edit` (`~/.clauderig/config.json`) |
| `doctor` | Health-check environment + sync + worktree discipline (`--fix` repairs) |
| `ui` | Interactive dashboard |

The worktree and prune verbs (`rig worktree`, `rig prune`) live in
[`rig`](/rig/verbs) — claudeRig wires the *guard* that makes them the default
path. See [Worktree discipline](#worktree-discipline) below.

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

`clauderig guard` (a PreToolUse hook) and `rig worktree` make worktrees and
PRs the default path for Claude Code, and stop a session from scrambling your VS
Code chat history by moving its working directory. Chat history is keyed to the
folder path, so the model edits from one pinned window while worktrees open in
their *own* window for review only.

```sh
rig worktree new <branch>   # sibling checkout off mainline (prints the path)
rig worktree new <branch> --open    # …and open a review window for this run
rig worktree new fix/x --base release-1
rig worktree list           # this repo's worktrees (alias: ls)
rig worktree open <branch>  # (re)open a worktree's review window (branch or path)
rig worktree rm <branch>    # remove the worktree, keep the branch (-f if dirty)
```

Worktrees live at `<parent>/<repo>-worktrees/<branch>` — a **sibling** of the
repo, so they never clutter the primary checkout and each gets its own
review-window history. `new` never moves the session's cwd; it prints the path
and, when opted in, opens a separate window.

### Configuring the review window

Because `worktree` is a [`rig`](/rig/verbs#git-worktree-verbs) command, the
review-window behavior is configured in **`.rig.json`** via `rig config set`, not
in claudeRig. By default `new` does **not** open a window — opt in per run with
`--open`, or always with the `worktree.autoOpen` key:

```sh
rig config set worktree.autoOpen true       # always auto-open (like --open)
rig config set worktree.openCmd "cursor -n"  # open Cursor instead of VS Code
```

- **`autoOpen`** (default `false`) — whether `new` opens a window at all.
  `--open`/`--no-open` override it per run; `worktree open` always opens.
- **`openCmd`** (default `code -n`) — the program plus any flags; the worktree
  path is appended as the final argument and run directly (no shell). Examples:
  `code -n`, `cursor -n`, `code-insiders -n`, `subl -n`, `idea`.

See [rig → Configuration](/rig/configuration#worktree) for the full details.
When the opener isn't on `PATH`, `new`/`open` print the command to run instead.

::: tip
See the [worktree-discipline doc](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/WORKTREE-DISCIPLINE.md)
for the guard rules and the full model.
:::

::: tip
See the [design doc](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/CLAUDERIG-DESIGN.md)
for the full picture.
:::
