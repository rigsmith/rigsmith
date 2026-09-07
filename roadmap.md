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

### shiprig + the `release` action — closing the release-please DX gap

**Context.** The `release` composite action already gives the release-please
experience on changesets: a standing **Version Packages** PR that previews the
next release and publishes when merged (see `docs/GITHUB-ACTIONS.md`). What
release-please does *beyond* that is mostly about closing loops — telling
contributors what happened to their change, and letting maintainers steer the
release moment. This is the list of what would take the action and shiprig
from "parity with changesets/action" to "better than either". Roughly ordered
by leverage within each group.

**Action — close the loop back to contributors**

- **"Released in" comments on source PRs.** After `publish`, comment on every
  PR whose changeset shipped (`Released in pkg@1.2.3`) and apply a label
  (release-please's `autorelease: tagged`). This is the single most-loved
  release-please feature. The changeset→commit→PR mapping already exists in
  the changelog-enrichment step, so it's plumbing, not new analysis.
- **Rendered changelog preview in the feature PR.** The `require-changeset`
  sticky comment today only says present/missing. Render the exact changelog
  line each changeset will produce, per package, with the bump level, so bad
  summaries get fixed in review instead of in the Version PR.
- **A real Version PR body.** Today the body is `shiprig status` in a code
  block. Render markdown: one section per package (old → new version), the
  changelog entries, links to the originating PRs. Put a hidden marker above
  the generated region so regeneration never clobbers anything a human added
  below it.

**Action — control over the release moment**

- **Hold and skip labels on the Version PR.** A `release:hold` label stops the
  action from force-updating the release branch while someone hand-edits it.
  Per-package exclusion (a checkbox list in the body, or a label) lets one
  package ship while another waits.
- **Snapshot publishes from feature PRs.** shiprig already does snapshot
  versioning. Wire a `/snapshot` comment or a `snapshot` label that publishes
  `0.0.0-pr<N>-<sha>` so reviewers can try the change before merge (the
  pkg.pr.new idea). Big win for library repos.
- **Verified commits.** Push the version commit via the GraphQL
  `createCommitOnBranch` mutation instead of `git push`, so it carries
  GitHub's signature (green Verified badge) and satisfies signed-commit
  branch protection. changesets/action gained this after years of requests.
- **Job summary and dispatch.** Write the plan to `GITHUB_STEP_SUMMARY` so the
  run page shows what would ship without opening the PR. Add a
  `workflow_dispatch` input for publish-only reruns after a partial failure.

**CLI**

- **Explicit version override in a changeset.** A `version: 1.0.0` frontmatter
  key — the equivalent of release-please's `Release-As:` footer — for the
  "we're going 1.0 now" moment without faking a major bump.
- **`shiprig status --changelog`.** Print the exact markdown `version` would
  write, per package, touching nothing. The same renderer feeds the PR
  preview above; locally it answers "what does the changelog look like right
  now" instantly.
- **`changerig add --from-pr`.** Prefill package (from touched paths), bump,
  and summary from the branch's open PR title. Most changesets are a
  rephrasing of the PR title anyway.
- **Automatic changesets for dependency bumps.** Dependabot/Renovate PRs never
  carry changesets, so they either block on the gate or need the skip label.
  Generate a patch changeset from the lockfile diff, or let the gate
  auto-waive a configurable list of bot authors.
- **Changeset lint.** Reject unknown package names, empty summaries, and
  invalid bump levels at `add` time and in the gate. Silently misfiled
  changesets are the most common "why didn't my change ship" bug in the Node
  ecosystem.

**Dogfooding**

- **Wire the action into rigsmith itself.** `docs/GITHUB-ACTIONS.md` notes the
  action isn't used by the repo that builds it (GoReleaser-only, no
  `.changeset/`). Moving rigsmith onto `.changeset/` + the action catches
  action bugs before the polyglot consumers do, and gives the most active
  repo the running changelog preview.

**If only three ship:** released-in comments, changelog preview in the feature
PR, snapshot publishes. Those are the ones contributors notice every day.

### Design sketch: moving the whole release onto the GHA side

**The question.** Can shiprig deliver the release-please experience end to end
in CI — standing PR, release on merge, tags + notes, *and* the built artifacts
release-please leaves as an exercise — without losing the one-machine
`shiprig release` flow?

**What already exists.** More than the README's "artifacts not yet wired" note
suggests. The `release` pipeline has `--yes` (answer every confirm gate),
`--from`/`--to` (resume at / stop after a step), `--dry-build` (build
artifacts, publish nothing), `--local`/`--rehearse` (prove it works before
anything leaves the machine). The forge layer (`internal/shiprig/forge`)
creates GitHub/GitLab/Gitea releases and attaches assets idempotently
(`gh release upload --clobber`). The `release` action already does the
standing Version PR and publish-on-merge. So the state machine is there; the
gaps are that the action calls bare `shiprig publish` rather than the
pipeline, and nothing fans out builds.

**Target shape.** Three phases. shiprig owns every *decision* (what releases,
at what version, with what notes); GitHub Actions owns the *compute*.

1. **Plan + version** — `push` to main. The action runs `shiprig version` and
   keeps the Version Packages PR current. Unchanged from today.
2. **Publish + tag** — merge of that PR. The action runs
   `shiprig release --yes --to push` instead of bare `shiprig publish`: commit,
   publish to registries, tag, push, stop. It emits a JSON *release plan*
   (packages, versions, tags, notes) as a step output alongside the existing
   `publishedPackages`.
3. **Build + attach** — a matrix job keyed on that plan. Each OS runner builds
   its slice and uploads a workflow artifact. A final job downloads them and
   runs `shiprig release --yes --from release`, which creates the forge
   release with the changelog notes and attaches the assets.

**Why the split at step 3.** One runner cross-compiles Go and .NET fine, but
macOS signing/notarization, Windows signing, and anything with native deps
need their own OS. release-please sidesteps this by not building at all.
shiprig does better by answering "what to release" and letting the workflow
answer "where to build it". The local `shiprig release` path is unchanged: the
same `.changeset/release.jsonc` drives both; in CI the `build` step is
disabled in favour of the matrix and the confirm gates are answered by
`--yes`.

**Two things to get right.**

- **Draft first, publish after assets land.** Create the forge release as a
  draft in step 3's final job, upload, then flip it to published. Otherwise
  watchers get a release notification with no binaries for ten minutes — the
  classic release-please complaint. Small addition: a `draft` flag on the
  release step and a `--publish-draft` finisher (or fold into `--from release`).
- **JSON plan as the contract between phases.** `shiprig release --plan-output
  plan.json` (or `shiprig status --output json` post-version) is what the
  matrix reads. Keep it stable and documented; it is the action's public API.

**Concrete work list** (small — most is wiring):

- Action: a `mode`/`to` input so it runs the pipeline instead of bare publish;
  a `releasePlan` JSON output.
- Action: a second small composite (`release-attach`) or a documented workflow
  for the attach job, consuming the plan + workflow artifacts.
- Pipeline: `draft` on the release step; `--plan-output`; make `--from release`
  accept the plan file so it doesn't need to re-derive state from git.
- Docs: replace the README's "artifacts not yet wired" with "artifacts build in
  your matrix; shiprig attaches them" and an example `release.yml` showing all
  three phases.

**What this buys over release-please.** Everything it does (standing PR,
release on merge, tags + notes, released-in comments per the section above)
plus the artifacts leg — and it stays polyglot, one pipeline driving NuGet,
npm, crates.io, and Go tags.
