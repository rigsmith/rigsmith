# Roadmap

Progress on active rigsmith work, followed by ideas that are not committed scope.
Released work lives in the changelog; implementation contracts live in `docs/`.

## Active work

### V2: separate `clauderig` and `codexrig` with shared infrastructure

**Current position:** the shared engine, Claude adapter, durable queue,
retained snapshots, conflict recovery, confirmed publication and worker loop are
merged into `codex/v2`. Startup history checks (#367) and Windows command ownership
at creation (#368), Unix worker-death supervision (#369), and persistent restart
fencing (#370) and canonical Git command-runner plumbing (#371) are merged.
Canonical workflow supervision (#372) and Linux/macOS fenced-store recovery
(#373) and Windows recovery (#374) are merged. Queue capacity reporting and receipt
compaction (#375), artifact-store limits and interrupted archive-write cleanup
(#376) are merged. Writer-owned workspace cleanup is in review; reference-aware
sealed-artifact cleanup follows.

The release path is now: finish capacity/cleanup controls,
enable opt-in queued Claude sync, then connect the separate `codexrig` adapter.
Ordinary Claude commands and hooks still use their existing synchronous workflow.

| Milestone | Status |
| --- | --- |
| Secret scanning, transcript chunking, and byte preservation | Complete on the v1 foundation. |
| Compatibility baseline and synchronous services | Merged into both `main` and `codex/v2` through [#299](https://github.com/rigsmith/rigsmith/pull/299) and [#301](https://github.com/rigsmith/rigsmith/pull/301). |
| Claude root/file adapter and policy extraction | Merged into `codex/v2`: [#304](https://github.com/rigsmith/rigsmith/pull/304). |
| Shared file processing and restore mechanics | Merged into `codex/v2`: [#306](https://github.com/rigsmith/rigsmith/pull/306), including the directory-link review fix. |
| Shared session/metadata and publication boundaries | Merged into `codex/v2`: [#307](https://github.com/rigsmith/rigsmith/pull/307), including review fixes. |
| Store locks and durable queue | Merged: #308/#309. |
| Sealed capture, retained commits and publication | Merged: #310–#319; existing Git/`gh` integration and owned process cleanup through #327. |
| Queue execution and retry/blocking policy | Merged: #332/#336. |
| Retained metadata and native append recovery | Merged: #337/#341. |
| Retained chunked-transcript recovery | Merged: [#342](https://github.com/rigsmith/rigsmith/pull/342): verify immutable parts, recover bounded append conflicts, preserve chunking. |
| Ordinary file conflict policy | Merged: [#343](https://github.com/rigsmith/rigsmith/pull/343): choose the newer proven Git snapshot; equal/unknown origins remain blocked. Both conflict sides are scanned; malformed profile roots are excluded. |
| Canonical staging merge recovery: staged resolutions | Merged: [#344](https://github.com/rigsmith/rigsmith/pull/344). Audit and finish an already-staged merge before retrying a committed batch; preserve the index and unstaged files. |
| Canonical staging merge recovery: staged completion before capture | Merged: [#346](https://github.com/rigsmith/rigsmith/pull/346). Audit and finish an already-staged merge before retaining fresh capture ancestry. |
| Unresolved merge recovery: private audited plan | Merged: [#347](https://github.com/rigsmith/rigsmith/pull/347). Recreate supported conflicts privately, validate index provenance, and export an audited candidate without changing staging. |
| Unresolved merge recovery: safe application and restart | Merged: [#348](https://github.com/rigsmith/rigsmith/pull/348). Seal a repair intent, preserve later edits, durably install files/index, and resume interrupted staging. |
| Unresolved merge recovery: queue integration | Merged: [#357](https://github.com/rigsmith/rigsmith/pull/357). Resume the exact sealed repair across staging, commit and cleanup before capture/publication. |
| Windows CI parallelization | Merged: [#358](https://github.com/rigsmith/rigsmith/pull/358). Run the existing test groups concurrently while preserving coverage and the required aggregate check. |
| Manual-sync queue coverage: shared checkpoint (6b.5a) | Merged: [#359](https://github.com/rigsmith/rigsmith/pull/359). Seal candidate membership before capture and acknowledge only fully covered batches; preserve later arrivals, partial coverage and saved recovery work. |
| Manual-sync queue coverage: Claude integration (6b.5b) | Merged: [#363](https://github.com/rigsmith/rigsmith/pull/363). Capture fresh session/subagent evidence and verify the exact remote snapshot before acknowledging complete batches. Internal service; command/hook wiring remains in rollout. |
| Worker loop, graceful stop and draining (6b.6a) | Merged: [#365](https://github.com/rigsmith/rigsmith/pull/365). Poll accepted work, honor durable retries, yield to foreground operations, and retain unfinished work on stop/restart. |
| Startup shared-history validation (6b.6b.1) | Merged: [#367](https://github.com/rigsmith/rigsmith/pull/367). Check freshly fetched destination ancestry before claiming work; reject uninitialized/unrelated stores without changing queue attempts or staging. |
| Windows child ownership at creation (6b.6b.2a) | Merged: [#368](https://github.com/rigsmith/rigsmith/pull/368). Close the suspended-child assignment gap and test abrupt owner death before/after command startup. |
| Unix parent-death supervision (6b.6b.2b) | Merged: [#369](https://github.com/rigsmith/rigsmith/pull/369). Explicit supervisor entry point, inherited staging lease, queued phase/retry integration and forced worker-death tests at startup/running boundaries. |
| OS restart fencing and lifecycle validation (6b.6b.2c) | Merged: [#370](https://github.com/rigsmith/rigsmith/pull/370). Persist command intent before process creation; clear only after verified cleanup. Block replacement writers after supervisor/owner failure, including asynchronous Windows termination. |
| Canonical Git command-runner plumbing (6b.6b.2d.1) | Merged: [#371](https://github.com/rigsmith/rigsmith/pull/371). Shared runner selection for buffered Git and backup attribute commands, with supervised byte-preservation tests. Streaming/interactive calls reject selection; workflow adaptation follows below. |
| Canonical workflow supervision (6b.6b.2d.2) | Merged: [#372](https://github.com/rigsmith/rigsmith/pull/372). Bind active staging leases across capture/sync/publication/pull/merge, retain cleanup failures across fallback helpers, and stop later commands/file writes. Supervised manual sync confirms publication before queue acknowledgement; interactive tools remain refused. |
| Linux/macOS fenced-store recovery (6b.6b.2d.3a) | Merged: [#373](https://github.com/rigsmith/rigsmith/pull/373). Seal process-group ownership before Git starts; recover only after OS proof under the existing store lock. Preserve capture and queue state; no command/hook wiring. |
| Windows fenced-store recovery (6b.6b.2d.3b) | Merged: [#374](https://github.com/rigsmith/rigsmith/pull/374). Recover durable prelaunch/cleanup phases; unconfirmed jobs require a verified kernel restart. Same-boot job disappearance and legacy records never authorize clearing. |
| Queue capacity and receipt compaction (6b.7a) | Merged: [#375](https://github.com/rigsmith/rigsmith/pull/375). Report queue headroom and remedies; explicitly compact completed receipts under a durable producer replay cutoff. |
| Artifact-store capacity and interrupted archive writes (6b.7b.1) | Merged: [#376](https://github.com/rigsmith/rigsmith/pull/376). Optional per-store sealed-byte limits, capacity reporting and exclusive cleanup of interrupted archive-write files. |
| Writer-owned build/publication workspace cleanup (6b.7b.2a) | In review (current PR). Hold staging, capture, seed and commit leases; reclaim only reserved disposable workspaces. Preserve sealed artifacts, recovery stores, queue-parent confirmations and relocated OS-temp scratch. |
| Referenced sealed artifacts (6b.7b.2b) | Next. Prove which captures/seeds/commits are disposable against pending queue work and recovery dependencies before deleting them; reclaim queue-parent confirmation scratch under queue-worker ownership. |
| Opt-in queued Claude hooks | Planned after queue/recovery validation. |
| Codex adapter and separate `codexrig` executable | Planned as the second consumer of the shared layers. |

The v1/v2 foundation was aligned after v1.15.1; that alignment did not publish a
new release. Subsequent adapter and queue work targets `codex/v2`. Preserve
ClaudeRig's commands, hooks, state, and backup formats, and keep the two tools'
configuration and backup repositories separate.

Delivery order: reuse existing Git/`gh` authentication, finish queue execution and
recovery, validate lifecycle/capacity and opt-in queued Claude sync, then connect
the separate Codex adapter. SSH-agent discovery, generic credential providers and
new keychain configuration are deferred; they do not block this sequence.

See the [detailed roadmap](docs/CLAUDERIG-SHARED-LAYERS-ROADMAP.md) for milestones,
PR links, compatibility gates, and queue rollout requirements. Update its status
and this summary with each implementation PR and merge.

Merged: [#339](https://github.com/rigsmith/rigsmith/pull/339) for v1 and
[#340](https://github.com/rigsmith/rigsmith/pull/340) for v2 preserve concurrent destinations and missing-destination no-ops, report unexpected link-operation errors, and validate restored link
paths in older/manually edited backups, with directory-confined creation and
relative targets.

Merged in [#341](https://github.com/rigsmith/rigsmith/pull/341): retained append recovery for native JSONL transcripts and memory files.
It keeps both machines' additions when neither changed the shared history and
blocks conflicting UUIDs. [#342](https://github.com/rigsmith/rigsmith/pull/342) added bounded recovery for canonical chunked
transcripts, including the default chunking threshold. [#343](https://github.com/rigsmith/rigsmith/pull/343) adds ordinary-file snapshot ordering.
Staged canonical merge completion merged in [#344](https://github.com/rigsmith/rigsmith/pull/344); staged completion before fresh capture merged in [#346](https://github.com/rigsmith/rigsmith/pull/346). Private unresolved-merge planning merged in [#347](https://github.com/rigsmith/rigsmith/pull/347). Recoverable file/index application merged in [#348](https://github.com/rigsmith/rigsmith/pull/348); queue recovery integration merged in [#357](https://github.com/rigsmith/rigsmith/pull/357). Queued hooks stay disabled.

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
