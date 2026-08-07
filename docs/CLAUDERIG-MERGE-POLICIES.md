# clauderig — divergence merge policies

*Distilled from the 2026-08-07 Air/Pro incident. This is the spec source for the planned
`clauderig merge` command ([CLAUDERIG-UI-PLAN.md](CLAUDERIG-UI-PLAN.md), Phase 0).*

## The incident

Johns-MacBook-Air13 sat 10–15 sync commits ahead / 65 behind Johns-MacBook-Pro16 for a
full day. `clauderig pull` is ff-only, so it failed — but only into SessionStart hook
stderr. Nothing user-visible showed it: `clauderig status` and `clauderig-devices.json`
both said "synced minutes ago", because they record last *push*, not health. The staging
repo had the Pro's data fetched the whole time; it just never merged or applied.

## Resolution policies

Every conflict in the hand-merge fell into a mechanical policy — none needed judgment.
`clauderig merge` should encode exactly these and exit nonzero listing residual conflicts
only when a file matches no policy:

| File class | Policy |
|---|---|
| `lastUpdated`-style fields (`clauderig-devices.json`, `extensions-blocklist.json`, desktop `manifest.json`s) | Newest timestamp wins |
| `memory/MEMORY.md` (any project) | Union keyed by memory filename: keep the local line text for entries both sides have, append the other machine's entries for files only it has, splicing each after its predecessor entry when that anchor exists |
| Session `*.jsonl` | Append-only, so superset wins. A session resumed on both machines yields prefix + divergent tails → union of lines, local side first |
| `clauderig-manifest.json` | Merge per-key. The conflict is typically only `claudeVersion` (either side is fine); the `projects` map auto-merges entries from both machines |

Hazards learned the hard way:

- **Never resolve `clauderig-manifest.json` with whole-file `checkout --ours`** — that
  silently drops the other machine's auto-merged `projects` entries. Same trap for any
  file where git auto-merged most hunks and conflicted on one.
- **Merge, don't rebase.** Rebase replays every sync commit and re-conflicts on the same
  files each time (15 commits × same MEMORY.md conflict). A single `merge --no-ff` pass
  resolves each file once.

## Applying to `~/.claude` after merge

`Restore` copies every staged file over the target **unconditionally** — no
newer-than check, no skip-if-exists (`engine/restore.go`, the `copyFile` loop). There is
no live-session guard either, unlike `account switch`. The consequence isn't "never
restore while Claude runs" — it's narrower: any session **active since the last sync**
has its transcript in staging as a stale snapshot, and restore rolls the live
`.jsonl` back over the top. Restore should grow the same live-sessions guard `switch`
has (refuse or skip files belonging to running sessions). Until then, the safe apply is
additive — copy only files that don't exist locally, plus deliberate per-file overwrites
(e.g. the unioned MEMORY.md). The planned `clauderig peek <device> materialize`
formalizes additive-only.

## Reading a peer's sessions without merging

After any fetch, the peer's sessions are already in the staging repo's object store:

```sh
git -C ~/.clauderig/repo show origin/main:cli/projects/<project-dir>/<session-id>.jsonl
```

This is the basis for `clauderig peek` and the UI's remote-session browser — read-only
browsing of any device's history with zero merge risk.

## Registry hygiene

A ghost device `this` (registered pre-hostname-detection, June 2026) lived in
`clauderig-devices.json` until 08-07. Registration should reject an unresolved hostname,
and `clauderig device rm <name>` should exist for cleanup.
