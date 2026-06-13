# Worktree discipline (clauderig guard + worktree)

Two clauderig features that make **worktrees and PRs the default path** for Claude
Code, instead of an afterthought — and stop Claude from scrambling your VS Code
chat history by moving the session's working directory.

## The problem

1. **Chat history is keyed to the folder path.** Claude Code stores a session
   under `~/.claude/projects/<flattened-cwd>/` — the working directory with every
   non-alphanumeric character turned to `-` (see `clauderig/internal/project`).
   Two different folder paths are two different histories. There is no flag that
   makes one conversation appear under two paths.
2. **`EnterWorktree` moves the session's cwd.** When Claude enters a worktree it
   relocates the *current* session into the worktree directory — which silently
   moves the conversation into a different history bucket and, in VS Code, jumbles
   which window owns which chat.
3. **Concurrent chats in one checkout clobber each other.** Two sessions editing
   the same working tree overwrite each other's uncommitted work.

## The model

> **One VS Code window pinned to the primary repo = one continuous chat. Worktrees
> are sibling checkouts opened in their own window for review only — never for
> chatting, never by moving this session's cwd.**

Claude keeps editing from the pinned window using absolute paths into the worktree
and `git -C <worktree> …`; the cwd (and the chat) never move.

## `clauderig guard` — the PreToolUse hook

Reads each tool call on stdin and blocks the ones that break the model. It is
**total**: anything it isn't sure about defers to Claude Code's normal permission
flow, so a bug can only ever fail *open*.

| Tool call | Verdict |
|---|---|
| `EnterWorktree` / `ExitWorktree` | **Deny** — moves the session cwd; use `clauderig worktree new` |
| `Bash` with a top-level `cd`/`pushd` out of the repo root | **Deny** — relocates the shell. A subshell `(cd … && …)` is the escape hatch |
| `Edit`/`Write`/`NotebookEdit` of **code** while on `main`/`master`/`trunk` | **Deny** — needs a branch + worktree + PR |
| `git commit` of **code** while on a base branch | **Deny** — inspects the staged (and `-a` tracked) files |
| Same edits/commits of **docs or root config** | **Allow** (defer) — low-risk paths pass |
| Anything while on a **feature branch** | **Allow** (defer) |

**Low-risk paths** (editable on a base branch without ceremony): any `*.md`/`*.mdx`,
the `docs/` and `.github/` trees, and top-level config (`*.toml`, `*.yml`, `*.json`,
`.gitignore`, `LICENSE`, …). Everything else is treated as code.

**Override** (let anything through on a base branch):

```sh
export CLAUDERIG_ALLOW_MAIN=1     # for the session
touch .claude/allow-main          # for the repo, until you delete it
```

Install / inspect — the scope is the command, no flags:

```sh
clauderig project install   # guard hook (.claude/settings.json) + CLAUDE.md guide — committed
clauderig local install     # same, but .claude/settings.local.json — gitignored
clauderig project status    # what's set up here
clauderig project uninstall # remove it
```

`local install` also adds `.claude/settings.local.json` to `.gitignore` (unless a
broader pattern already covers it), so a personal hook can't be committed by
accident.

`clauderig doctor` verifies all of this — guard installed, CLAUDE.md guide
present, local settings gitignored, plus the environment (`git`/`gh`/`code`, and
whether `clauderig` itself is on `PATH` so the hooks actually run) — and offers to
fix what it can (`--fix`, or pick interactively).

`project install` is the one-shot "protect this repo": it wires the guard at
**project** scope (`<repo>/.claude/settings.json`) — commit it and your team
inherits it (Claude Code asks to trust a project hook the first time) — and drops
the [CLAUDE.md guide](#clauderig-guide--teach-every-claude-context). `local` does
the same in the gitignored `settings.local.json`. The hook command is the bare
`clauderig guard`, portable across machines.

The **sync** hooks (SessionStart→pull, Stop→sync) are separate and global —
`clauderig global install` (aliased `clauderig hooks install`) writes them to
`~/.claude/settings.json`, where they ride clauderig's own sync.

## Scopes

`clauderig <scope> <action>` writes to the Claude Code settings tier that fits the
scope (`internal/settings`). Claude Code merges all tiers at runtime.

| Scope command | File | Installs | Travels via |
|---|---|---|---|
| `global` (alias `hooks`) | `~/.claude/settings.json` | sync hooks | clauderig sync |
| `project` | `<repo>/.claude/settings.json` | guard + guide | committed to the repo |
| `local` | `<repo>/.claude/settings.local.json` | guard + guide | nowhere (gitignore it) |

`project` and `local` are also the home for any future command that should affect
just this checkout — scope is the command, so a new repo-local action is just
another verb under them.

## `clauderig worktree` — the safe way to branch

```sh
clauderig worktree new <branch>   # sibling checkout off the repo's mainline + new VS Code window
clauderig worktree new fix/x --base release-1 --no-open
clauderig worktree list           # ls this repo's worktrees
clauderig worktree open <branch>  # (re)open a worktree's window for review
clauderig worktree rm <branch>    # remove the worktree (branch is kept)
```

Worktrees live at `<parent>/<repo>-worktrees/<branch>` — a **sibling** of the repo,
so they never clutter the primary checkout's file tree, and each has its own folder
path (its own review-window history). `new` **never moves this session's cwd**; it
prints the path and opens a separate window with `code -n`.

## The CLAUDE.md guide — teach every Claude context

The guard *enforces* the rules; a marker-delimited block in `CLAUDE.md` *explains*
them, so a session works with the guard instead of bumping into denials. **`project
install` / `local install` drop this block for you** — the standalone `guide`
command is only for cases they don't cover (a global block, or an explicit path):

```sh
clauderig guide install --global   # ~/.claude/CLAUDE.md (applies to every project)
clauderig guide install --path P   # an explicit CLAUDE.md
clauderig guide status             # is the block present?
clauderig guide uninstall          # remove just the block
clauderig guide show               # print the block
```

It owns only its block — the de-facto convention for a tool managing a region of a
shared instruction file — fenced by markers, and rewrites only that block:

```
<!-- BEGIN clauderig:worktree-discipline -->
…
<!-- END clauderig:worktree-discipline -->
```

Re-installing replaces it in place (idempotent), so a machine that pulls a newer
clauderig picks up the latest guidance on the next `install`. The `:slug` suffix
leaves room for clauderig to own further independent blocks later.

### Typical loop

```sh
clauderig worktree new feat/thing            # creates worktree + opens review window
#   …Claude edits files under .../rigsmith-worktrees/feat-thing by absolute path,
#   runs git via:  git -C .../feat-thing add -A && git -C .../feat-thing commit -m …
git -C .../feat-thing push -u origin feat/thing && gh pr create   # open the PR
clauderig worktree rm feat/thing             # after the PR merges
```
