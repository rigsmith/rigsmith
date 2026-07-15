# Commands

| Command | What |
|---|---|
| `init` | First-run wizard: remote (private), machine identity, roots, hooks |
| `sync` | Walk → redact → manifest → tripwire → commit → push (`--dry-run`) |
| `pull` | Fetch latest into the staging repo (no write to `~/.claude`) |
| `restore` | Restore here, rewriting paths (`--dir`, `--backup`, `--force`, `--prune`); nudges a Desktop restart when Code sessions come back |
| `status` | Sync state: remote, last sync, roots, hooks |
| `search` | Find a Claude Code session by title or content across live + synced history (alias `grep`); `--raw` grep lines, `--all` every file, `--live`/`--repo` scope, `-s` case-sensitive |
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

When a restore brings back Claude **Code** sessions, it reminds you to fully quit
and reopen Claude Desktop — Desktop only rebuilds its Code-tab list from the
restored session sidecars on startup, so a running app won't show them until it
restarts.

## Finding a session

Lost track of which chat had that work? `search` finds it by **title or
content**:

```sh
clauderig search "auth refactor"
```

It scans your Claude Code transcripts (`projects/**.jsonl`) across this machine's
live `~/.claude` **and** the synced staging repo — which may hold sessions from
other machines or older ones no longer live here — then groups the hits into
named sessions:

```
● Refactor the auth middleware
  a1b2c3d4 · 2026-07-02 · opus-4-8 · ~/Git/acme-api · desktop+repo
  17 match(es)   resume: cd ~/Git/acme-api && claude --resume a1b2c3d4-…
    …split the token check out of the request handler…
```

Each result carries the session's Desktop **title**, project, last-used date, and
a ready `claude --resume` command. Titles are matched too, so a topic word finds
the chat even when it isn't in the body, and results rank by relevance (title
hit, then match count, then recency). Matches inside injected skill-listing and
system records are ignored — so a word that appears in the skill catalog (like a
skill name) doesn't light up every session.

- `--raw` — grep-style line output instead of grouped sessions
- `--all` — search *every* file (config, skills, file-history, the Desktop dir),
  not just transcripts (implies `--raw`)
- `--live` / `--repo` — restrict to this machine or the synced repo
- `-s` / `--case-sensitive`

On a long search a live `scanning… N transcripts, K matches` status shows on the
terminal (and stays out of piped output).

::: tip Desktop "Chat" tab
Desktop **Chat**-tab conversations live server-side, not on disk — they never
appear here. `search` covers your Claude **Code** transcripts; a miss doesn't
prove a Chat-tab conversation is gone, so check claude.ai for those.
:::

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
