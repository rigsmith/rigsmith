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

Install / inspect:

```sh
clauderig hooks install           # adds the PreToolUse guard (+ SessionStart/Stop sync)
clauderig hooks status
```

The hook command is the bare `clauderig guard`, so it stays correct when
`settings.json` itself syncs to another machine.

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

## `clauderig guide` — teach every Claude context

The guard *enforces* the rules; the guide *explains* them, so a session works with
the guard instead of bumping into denials. It manages a marker-delimited block in
`CLAUDE.md` — the de-facto convention for a tool owning a region of a shared
instruction file — and rewrites only that block, never the rest:

```sh
clauderig guide install            # add/update the block in the repo's CLAUDE.md
clauderig guide install --global   # ~/.claude/CLAUDE.md (applies to every project)
clauderig guide install --path P   # an explicit CLAUDE.md
clauderig guide status             # is the block present?
clauderig guide uninstall          # remove just the block
clauderig guide show               # print the block
```

The block is fenced by:

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
