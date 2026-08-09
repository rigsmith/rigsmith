# clauderig — divergence merge policies

*Distilled from the 2026-08-07 Air/Pro incident. This was the spec source for
`clauderig merge` ([CLAUDERIG-UI-PLAN.md](CLAUDERIG-UI-PLAN.md), Phase 0), **shipped
2026-08-08** — see [Implementation](#implementation) for where each policy lives and the
one place the code reads the spec differently.*

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

`Restore` copies every staged file over the target — no newer-than check, no
skip-if-exists (`engine/restore.go`, the `copyFile` loop). The consequence isn't "never
restore while Claude runs" — it's narrower: any session **active since the last sync**
has its transcript in staging as a stale snapshot, and restore would roll the live
`.jsonl` back over the top.

**Fixed 2026-08-08.** Restore now has a live-sessions guard (`liveTranscripts` in
`engine/restore.go`). It reads `~/.claude/sessions/*.json` via
`account.RunningInstances` — the same process detection `account switch` uses — and
skips exactly `projects/<flattened cwd>/<sessionId>.jsonl` for each running session.

Two properties worth keeping:

- **Per-session, not per-project.** Only the file in flight is protected; other
  sessions in the same project still restore normally.
- **Skipped, not refused, and never silent.** A restore is mostly config and skills,
  which are safe to write — refusing the whole operation would be disproportionate. The
  skipped transcripts are named in the output, the way sync names oversized files, so it
  can't read as "everything restored".

A protected path need not exist yet: a session that has registered but not flushed its
first turn is exactly the one that must not have a stale copy dropped underneath it.

The safe apply for anything outside this guard is still additive — copy only files that
don't exist locally, plus deliberate per-file overwrites (e.g. the unioned MEMORY.md).
The planned `clauderig peek <device> materialize` formalizes additive-only.

## Reading a peer's sessions without merging

After any fetch, the peer's sessions are already in the staging repo's object store:

```sh
git -C ~/.clauderig/repo show origin/main:cli/projects/<project-dir>/<session-id>.jsonl
```

This is the basis for `clauderig peek` and the UI's remote-session browser — read-only
browsing of any device's history with zero merge risk.

**Shipped 2026-08-08** as `internal/clauderig/peek` plus `clauderig peek
list|show|materialize`. Two things the design had to work out that this section didn't
cover:

- **Sessions carry no machine tag.** The repo merges every machine's transcripts into
  one tree, so `peek <device>` as a positional made no sense. Attribution instead comes
  from each file's most recent `clauderig sync: <machine>` commit subject — the only
  record of provenance the repo has — surfaced as a `--device` filter. A merge or squash
  commit yields no machine rather than a guess.
- **One `git log` walk, not one per file.** A real repo holds ~700 transcripts;
  per-file attribution meant ~700 git processes. A single `log --name-only` over the
  transcript tree gives newest-first commits with their files, and the first appearance
  of a path is its attributing sync. Listing 723 sessions takes well under a second.

`materialize` is the only write and is strictly additive: `O_EXCL`, so it refuses when
the id already exists locally even if the file appears between the check and the write.
The local copy may be a session that is still running, and overwriting it would lose
every turn since the remote's snapshot — the same lesson as the live-session guard above.

## Registry hygiene

A ghost device `this` (registered pre-hostname-detection, June 2026) lived in
`clauderig-devices.json` until 08-07.

**Fixed 2026-08-08.** Both halves:

- **Registration refuses a placeholder.** `config.IdentityResolved` reports whether
  `ResolveName` found a real identity (a matching config entry, or a hostname) or fell
  through to `config.UnresolvedName` — still literally `"this"`, so an existing ghost is
  recognisable as the same bug. `sync` registers only when resolved, and says so plainly
  when it declines. Display paths keep the placeholder, because a status line still needs
  *some* name; only writes are gated.
- **`clauderig device remove <name>`** (alias `rm`) clears leftovers, alongside
  `clauderig device list`. The registry is synced, so a removal reaches the other
  machines on the next sync. It deletes no session data and doesn't stop that machine
  syncing — if it syncs again it simply re-registers, which the prompt says out loud when
  you target the machine you're on. Interactive confirm required; it refuses without a
  terminal, like every other destructive command here.

## Implementation

`internal/clauderig/merge` holds the policies as pure functions over the three sides of
a conflict, so each is testable from a literal with no git involved.
`internal/clauderig/commands/merge.go` does the git: fetch, `merge --no-ff`, read each
conflicted file's stages, apply, stage, commit.

| Policy | Matches | What it does |
|---|---|---|
| `devices-union` | `clauderig-devices.json` | union per machine; newer `lastSync` wins that row |
| `manifest-union` | `clauderig-manifest.json` | union `projects`; higher `claudeVersion` |
| `memory-union` | `MEMORY.md` | union by memory filename, local wording kept, remote-only entries spliced after their anchor |
| `transcript-superset` | `*.jsonl` | line union, local side first, shared prefix once |
| `newest-timestamp` | other `*.json` | whole file from the side with the newer `lastUpdated` |

### Where the code reads this spec differently

The table above lists `clauderig-devices.json` under "newest timestamp wins". The
implementation applies that **per device row, not per file**, for two reasons:

1. The file has no single top-level timestamp, so a whole-file comparison is undefined —
   each device carries its own `lastSync`.
2. Each machine only ever touches its own row (`devices.Touch`), so a whole-file pick
   would silently delete the other machine's row — exactly the hazard this document
   warns about for the manifest one section down.

### Deliberate non-goals

- **A file matching no policy is never guessed at.** It stays conflicted with git's
  markers intact, is named in the output, and the command exits nonzero with the repo
  left mid-merge so the user's own mergetool can open it. `clauderig merge --abort`
  backs the whole thing out.
- **Files already resolved stay resolved** when others can't be. A partial merge is
  progress; aborting would throw it away.
- **Delete/modify conflicts are residual.** Resurrecting a file the other machine
  deleted — or dropping one it kept — is a judgment call, not a mechanical one.
- **A JSON file with no `lastUpdated` is residual**, rather than being decided by a coin
  toss between two equally plausible sides.
