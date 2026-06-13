# clauderig

Sync your Claude Code setup across machines — config, skills, and session
history — to your own **private** git repo, and restore it on any computer with
paths corrected across OSes and secrets never leaked. Pick up where you left off
on a different machine.

The fourth rig: a single statically-linked Go binary, zero runtime deps,
installable by `curl | sh` / Homebrew / Scoop on any machine.

```sh
clauderig init                 # wizard: create/choose a PRIVATE repo, machine name, hooks
clauderig sync                 # snapshot → redact secrets → rewrite paths → commit → push
clauderig restore              # pull → rewrite slugs for this OS → merge (keeps local secrets)
clauderig restore --dir /tmp/x # restore the CLI payload into a folder (inspect, don't touch ~/.claude)
clauderig status               # remote reachability, last sync, per-root counts, hooks
clauderig pull                 # fetch latest into the staging repo (SessionStart hook target)
clauderig doctor               # health-check env + sync + worktree discipline (--fix to repair)
clauderig global install       # global sync hooks in ~/.claude (alias: clauderig hooks install)
clauderig project install      # protect THIS repo: guard hook + CLAUDE.md guide (committed)
clauderig local install        # same, but gitignored to this checkout
clauderig worktree new feat/x  # sibling worktree + new VS Code window; never moves this session
clauderig ui                   # interactive dashboard
```

## Worktree discipline

`clauderig guard` (a PreToolUse hook) and `clauderig worktree` make worktrees and
PRs the default for Claude Code, and stop it from scrambling your VS Code chat
history — which is keyed to the folder path — by moving the session's working
directory. The guard denies `EnterWorktree`, denies a `cd` out of the repo root,
and on `main` requires a branch+worktree+PR for code while letting docs/config
through (override: `CLAUDERIG_ALLOW_MAIN=1` or `touch .claude/allow-main`). It
fails open on anything it isn't sure about. `clauderig project install` sets a repo
up in one shot — the guard hook in `.claude/settings.json` plus a marker-managed
block in `CLAUDE.md` so every Claude context learns the rules (`local install` does
the same in the gitignored `.claude/settings.local.json`). See
[docs/WORKTREE-DISCIPLINE.md](../docs/WORKTREE-DISCIPLINE.md).

## What it does

- **Cross-OS path correction.** A session captured at `/Users/john/Git/x` resumes
  at `C:\Users\John\Git\x`. Project directory slugs and path values inside config
  are re-derived for the target machine (`core/pathmap`).
- **Secrets never leave the machine.** Secret-bearing fields are stripped before
  commit; a tripwire fails the sync loudly if one slips past. Restore merges the
  synced config back without clobbering your local secrets — a new machine
  re-authenticates.
- **Private repo, no exceptions.** The remote must be a GitHub repo that `gh`
  confirms is private — created with `gh repo create --private` or an existing
  one verified via `gh repo view`.
- **Allowlist, default-deny.** Only curated files sync; the ~12 GB Desktop cache
  tree is pruned, never descended.
- **Bounded repo.** 30-day retention on transcripts + a size-based history squash.

## Commands

| Command | What |
|---|---|
| `init` | First-run wizard: remote (private), machine identity, roots, hooks |
| `sync` | Walk → redact → manifest → tripwire → commit → push (`--dry-run`) |
| `pull` | Fetch latest into the staging repo (no write to `~/.claude`) |
| `restore` | Restore here, rewriting paths (`--dir`, `--backup`, `--force`, `--prune`) |
| `status` | Sync state: remote, last sync, roots, hooks |
| `global` | `install` / `uninstall` / `status` the global sync hooks in ~/.claude (alias: `hooks`) |
| `project` | `install` / `uninstall` / `status` this repo's guard hook + CLAUDE.md guide (committed) |
| `local` | same as `project`, but gitignored (`.claude/settings.local.json`) |
| `guard` | PreToolUse hook: require worktrees/PRs, block cwd-moving worktree tools (wired by `project`/`local`) |
| `worktree` | `new` / `list` / `open` / `rm` sibling worktrees, opened in their own review window (configurable; alias `wt`) |
| `guide` | `install` / `uninstall` / `status` / `show` the CLAUDE.md block standalone (e.g. `--global`) |
| `config` | `show` / `set-remote` / `set-prune` / `set-autorestore` / `set-worktree-open` / `set-worktree-opener` |
| `doctor` | Health-check environment + sync + worktree discipline; `--fix` repairs, or pick fixes interactively |
| `ui` | Interactive dashboard |

See [docs/CLAUDERIG-DESIGN.md](../docs/CLAUDERIG-DESIGN.md) for the design and
[docs/CLAUDERIG-ROADMAP.md](../docs/CLAUDERIG-ROADMAP.md) for status.

## Install

```sh
curl -fsSL https://rigsmith.sh | sh -s clauderig    # once the release exists
# or build from source (go.work workspace):
go build -o clauderig ./clauderig
```

Requires `git` and the GitHub CLI (`gh`, authenticated) for the private-repo gate.
