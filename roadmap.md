# Roadmap

Forward-looking ideas for the rigsmith tools. Nothing here is committed scope —
it's the "where this could go" list. Shipped work lives in the changelog and the
`docs/` design docs.

## Ideas

### A parallel-dev / multi-agent worktree hub (its own binary)

**The bigger vision behind worktree pinning.** The `-wt` launchers,
`clauderig worktree`, and the `-dev` active-route pin (PRs #67/#69) are the seed
of something larger than "a few worktree subcommands": a dedicated hub for
running many worktrees — and many agents — in parallel.

Picture a bare-invocation dashboard (same navigable-menu direction as the rig
tools' menus) that shows, across repos:

- **Every worktree**, with live **PR status** (open/merged/checks), **dirty vs
  clean**, and **merged/ahead/behind** state.
- **Which agent/session owns each** worktree — pairing with the session-spawning
  features so you can see who's working where at a glance.
- **One-key actions**: spawn a new worktree (+ session), switch to one, prune the
  clean+merged ones — the manual flow `clauderig worktree new/prune` already
  encodes, lifted into a single screen.
- **The `-dev` route pin front-and-center**: which worktree the `-dev` tools
  currently build from, switchable inline (today: `<tool>-wt --use` / the menu;
  see `core/devroute`).

**Why a binary, not more `clauderig worktree` verbs.** This is genuinely its own
domain — orchestrating parallel development across worktrees, repos, and agents —
distinct from clauderig's "sync my Claude setup across machines" charter. It's
on-brand with the established "navigable dashboard" direction (rig / clauderig /
changerig / shiprig all land on a hub menu), and it pairs naturally with the
session-spawning work. That combination earns a fifth rig.

Open questions to resolve before it's real:
- Scope: single-repo first, or multi-repo from day one?
- Where does cross-repo state live (the `-dev` route is already per-repo under
  `~/.local/state/rigsmith/` — does a hub need a registry of repos to watch)?
- How does it learn "which agent owns this worktree" — convention, a session
  registry, or integration with the spawn features?
- Relationship to `clauderig worktree`: does the hub absorb those verbs, or call
  into them (clauderig stays the worktree-mechanics owner, the hub is the view)?

### `clauderig resume` — open the session `search` just found

`search` already ends every result with the action for it, and there are three
of them because a session can be in three places:

- live in `~/.claude` → `resume: cd <cwd> && claude --resume <id>`
- in the synced repo only → `synced copy only — restore on this machine to resume`
- in the ledger only → `aged out of the synced window — the body may still be in
  the sync repo's git history`

The first is a line you copy and paste, which is a verb wearing a disguise.
`clauderig resume <id-or-query>` would resolve a session the way `search` does —
including by title, so `clauderig resume "windows runner"` works — and `exec`
`claude --resume` in the right cwd. That part is plumbing over code that exists.

The design work is the other two states, and it is the reason this is a roadmap
entry rather than a chore:

- **Repo-only.** The transcript is sitting in the staging tree and `claude` cannot
  read it there. Should `resume` offer to restore just that one session onto this
  machine first? A whole-tree `restore` is far more than the user asked for, so
  this probably wants a narrower "materialise this session" path that does not
  exist yet.
- **Ledger-only.** The body aged out. `resume` could recover the blob from git
  history — the same read `ledger backfill` already does — and materialise it, or
  it could simply explain and stop. Recovering it silently resurrects something
  retention deliberately dropped, so this needs a deliberate answer rather than a
  default.
- **Not on this machine at all.** A row recorded by another device names a session
  whose transcript never synced here. The honest answer is "run `clauderig sync`
  there", which `search`'s device roster already says — `resume` should not
  pretend to more.

Worth doing after the ledger has been in use for a while: the third case only
becomes common once rows outlive their transcripts.
