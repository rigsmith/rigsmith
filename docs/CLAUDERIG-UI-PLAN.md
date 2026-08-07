# clauderig UI — plan

*Drafted 2026-08-07 from the Air/Pro divergence incident (65 unmerged commits sat invisible
behind a failed fast-forward; resolved by hand). Decisions locked with John: menu-bar-first
app + window, CLI stays the engine, lives in this repo under `ui/`.*

## Shape

An Avalonia app (Foundation platform, submodule as usual) with two faces:

- **Tray icon** — the ambient face. Green (synced) / amber (behind remote) / red (diverged
  or last sync failed). Menu: sync now, per-device freshness, open window.
- **Window** — the interactive face: device board, activity feed, conflict resolution,
  remote-session browsing, account switching.

The UI owns no sync logic. It shells out to `clauderig` and renders; the Go CLI remains the
single implementation of sync, redaction, path rewriting, and merging. Same seam philosophy
as Tweed's engine split.

## Why (what the incident proved)

1. **Divergence is invisible.** `clauderig-devices.json` records *last push*, not health.
   This Air showed "synced 5 minutes ago" while 65 commits behind all day. The registry
   can't express ahead/behind, and the failed pull surfaced only in a SessionStart hook
   message nobody reads.
2. **Sync failures die in hook stderr.** A Jul-27 qbo session shows the Stop-hook sync
   refusing to run for days-worth of turns ("Secret tripwire: 12 value(s) look like
   credentials"). No surface ever showed it.
3. **Conflict resolution has correct answers a tool can encode.** Every conflict in the
   hand-merge fell into a policy: timestamps → newest wins; `MEMORY.md` → union keyed by
   memory filename; transcripts → superset (append-only line union); manifests → union of
   entries. None needed judgment — they needed a button. Full spec:
   [CLAUDERIG-MERGE-POLICIES.md](CLAUDERIG-MERGE-POLICIES.md).
4. **Reading another machine's sessions shouldn't require merging.** The Pro's chat was
   readable via `git show origin/main:<path>` the whole time. That's a viewer feature.
5. **Ghost devices** — the `this` entry (removed 08-07) came from pre-hostname-detection
   registration; there's no `device rm` and no validation.

## Phase 0 — CLI groundwork (Go, no UI yet)

Everything the UI needs that the CLI can't say today:

- `--json` on `status` (and `account list`, `search`): machine-readable output including
  **new divergence fields** — ahead/behind vs origin, whether a merge would conflict, last
  sync outcome per root.
- **Sync journal**: every sync/pull/restore appends a JSONL record (when, machine, files
  written, redactions, aged-out, LEAK refusals, error). The activity feed reads this;
  it also makes hook-failures durable instead of stderr-only.
- `clauderig merge`: encode the resolution policies above. Exits nonzero listing residual
  conflicts only when a file matches no policy. This is the engine behind the UI's
  one-click **Resolve**.
- `clauderig peek <device> [list|show <session>|materialize <session>]`: read sessions
  straight from `origin/main` blobs without merging; `materialize` copies one into
  `~/.claude/projects/` (additive, never overwrites).
- `clauderig device rm <name>` + reject registration when hostname resolution fails
  (the `this` glitch).
- Restore safety: `restore` copies unconditionally with no live-session guard
  (`engine/restore.go`); give it the `account switch` sessions-guard — skip or refuse
  files belonging to running sessions instead of rolling their transcripts back.

## Phase 1 — Tray + status window

- TrayIcon with the three-state health color, driven by polling `status --json`
  (30–60s; immediate refresh after any action).
- Device board: one card per device — last sync, ahead/behind, OS, Claude version,
  staleness coloring. (Registry data + journal.)
- Activity feed: recent syncs across machines with outcomes; failures and secret-tripwire
  refusals rendered as first-class rows, not buried.
- "Sync now" / "Pull" actions with streamed CLI output in a drawer.

## Phase 2 — Resolve + browse

- Divergence banner → **Resolve** button → `clauderig merge`, showing the per-file policy
  ledger (what was unioned, which timestamp won). Residual conflicts get a two-sided
  picker; never a raw conflict-marker editor.
- Remote session browser: per-device session lists via `peek`, read-only transcript
  rendering, **Bring to this Mac** → `materialize`.
- Search across live + synced sessions (`clauderig search --json`) with the same viewer.

## Phase 3 — Accounts

- UI over `clauderig account`: logins list, active credential, capture, switch, remove.
- Switch does the **both-halves swap** (Keychain + `oauthAccount`) per
  [CLAUDERIG-ACCOUNTS.md](CLAUDERIG-ACCOUNTS.md), and refuses while a Claude session is
  live.
- Desync detector: the `groveConfigCache`-keys heuristic (see the identity-desync
  section of [CLAUDERIG-ACCOUNTS.md](CLAUDERIG-ACCOUNTS.md)) as a health check with a
  "resync" action — catches the artifact-went-to-wrong-account failure before it bites.

## Open

- **Name.** Unnamed; "the clauderig UI" until John names it.
- Tray parity on Windows/Linux (Avalonia TrayIcon covers all three; verify behaviors).
- Whether Tweed later embeds the same status readout (it can — same `--json` seam).
- Auto-resolve-on-pull: once `clauderig merge` is trusted, the SessionStart hook could
  invoke it instead of failing the fast-forward. Decide after the button has mileage.
