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
  run `rig worktree new <branch>`. It creates a sibling checkout at
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

## What a change has to update

Work through this before opening a PR. Most changes touch only the first two
rows; the point of the list is that the rest are easy to forget, and each one
was in fact forgotten at least once.

| If the change… | Update |
|---|---|
| **anything a user would notice** | a changeset — `changerig add`. No changeset means no changelog entry, and the release ships silently |
| touches behavior | tests, including the gated end-to-end ones where they exist (`RIG_STACK_E2E=1`) |
| adds or renames a **verb or flag** | the command's `Short`/`Long`, the parent group's help block (and re-check its column alignment), tab completion if it takes an argument |
| is reachable from the **menu** | `rig ui` — the item, its one-line description, and any prompt copy that names what it will do |
| adds or changes a **config key** | the JSON schema in `site/public/schemas/`, the scaffold template the tool writes, `knownKeys` in `internal/rig/config` for a top-level `.rig.json` key, and `site/rig/configuration.md` |
| changes what a **verb does** | `site/rig/verbs.md`, the feature's own page if it has one, `README.md`, `cmd/<tool>/README.md` |
| changes how an **agent** should drive the tool | `~/.claude/skills/rigsmith-tools/SKILL.md` — including its `description`, which decides whether the skill fires at all |
| changes a **design decision** | the `docs/*-DESIGN.md` that recorded it |

Two things worth checking every time, because neither announces itself:

- **Examples in docs use generic names** — `acme`/`you`, not a real fork. Real
  repository names have leaked into published docs more than once.
- **Claims in docs still match the code.** "A verified binary, seconds not
  minutes" stopped being true the moment a custom pin could fall back to a
  source build. Prose ages badly and nothing tests it.
