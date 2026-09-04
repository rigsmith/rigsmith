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

### `rig flight` — what is in flight, and what has been stranded

**The audit half of the worktree hub above, shippable long before the hub is.** The
hub is a live dashboard you sit in front of; this is one command you run occasionally
that answers the three questions that actually go wrong, and exits.

Motivated by a real incident (Tweed, Sep 2026). Five or six agents were spawned across
worktrees, a few tangents were chased, and the repo was set aside for weeks because it
felt unmanageable. A read-only review found that almost nothing was wrong — but four
things were genuinely invisible:

- **A branch with 30 commits and no PR.** `pr-review-loop`: finished work, Copilot
  reviews addressed, pushed — and nothing anywhere pointed at it. A pushed branch with
  no PR does not appear in any list you look at.
- **~1,250 uncommitted insertions across four worktrees.** Agents that finished and
  exited without pushing, so a worktree was the only copy.
- **A campaign split across two remotes.** Six composer commits landed on three
  branches across `origin` and a second remote, so it read as missing when it was
  merely scattered.
- **Branches tracking nothing.** `git log --branches --not --remotes` looked alarming
  until 13 of 15 "unpushed" commits turned out to be machine-written session captures.

None of that needs a dashboard to catch. It needs one command:

```
$ rig flight
  ⚠ pr-review-loop           30 commits ahead, no PR          brightshore
  ⚠ send-affordance          509 uncommitted insertions       worktree only
  ⚠ command-blocks           tracks nothing, 9 dirty
  · 10 worktrees             machine-generated (tweed/*), clean — ignored
  ✓ 5 branches               PR open
```

The checks, each cheap:

- branches ahead of the default branch with **no open PR**
- worktrees with **uncommitted changes**
- branches with **no upstream**
- commits on **no remote at all**, with machine-generated prefixes filtered out
- work split across **more than one remote** in the same repo

**Why it earns its place separately from the hub.** The hub is a place you go; this is
a thing that tells you. It suits a weekly habit, a pre-context-switch check, or a CI
job that comments once a week — and it is a few hundred lines against the hub's
"fifth rig". It is also the natural first consumer of whatever registry the hub would
need, so building it informs that design rather than pre-empting it.

Open questions:
- Repo-local, or does it read the same repo registry the hub would want?
- Which prefixes count as machine-generated — convention (`tweed/*`), config, or
  "authored by a bot identity"?
- Does it grow a `--fix` that parks dirty worktrees onto `wip/` branches and pushes
  them, or does it stay strictly read-only and print the commands?

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
