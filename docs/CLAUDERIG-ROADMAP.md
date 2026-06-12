# clauderig roadmap

Status of the tool after the initial build + live end-to-end validation. The core
round-trip (sync → private GitHub repo → restore with cross-OS path correction and
secret safety) is built, tested (84 tests incl. two gated e2e tests — a full
round-trip and a mac↔windows portability check), and proven live. Run the gated
e2e with `CLAUDERIG_E2E=1 go test ./clauderig/internal/e2e/`. See
[CLAUDERIG-DESIGN.md](CLAUDERIG-DESIGN.md) for the spec.

Legend: ✅ done · 🚧 in progress · ⬜ not started

## Functional gaps (wanted before daily use)
1. ✅ **Apply retention.** 30-day window enforced on `projects/` at sync (was copying all history — the live run pushed 512 MB).
2. ✅ **Incremental sync.** Skip re-copying unchanged transcripts (mtime/size) instead of rewriting the whole tree each sync.
3. ✅ **Mirror-delete on restore.** Remove files deleted upstream — scoped to authoritative config dirs (skills/commands/agents/plans), never `projects/` (additive), behind `--prune`.
4. ✅ **Detect Claude Code version.** Stamp the real version in the manifest (was `""`) for the skew warning.
   - ✅ **Always-prune config option** (`alwaysPrune` / `config set-prune` / `init --prune`): make `--prune` the restore default; `--prune=false` overrides per-run.

## Polish / UX
5. ✅ **Distribution.** goreleaser build+archive for `clauderig`, `install.sh` target, version stamping (`-X main.version`), module + tool READMEs. (Homebrew tap still commented out pending the public repo, same as the other rigs.)
6. ✅ **Desktop `config.json` preferences.** Synced via a keep-only filter (`engine.keepOnly`) that retains just `preferences`, dropping the volatile caches/tokens the app constantly rewrites.
7. ✅ **Conflict resolution.** On a rejected push (remote advanced), sync fetch+merges; clean merges auto-reconcile, real conflicts hand off to `git mergetool` (interactive only; non-interactive aborts with a clear message). No comparison tool built.
8. ✅ **Richer TUIs.** Restore now prints a preview (target, source version, sample slug rewrites, project count) and prompts interactively (huh) for non-empty targets — back up / restore in place / abort; non-interactive defaults to abort.
9. ✅ **Device registry.** Synced `clauderig-devices.json`; each machine touches its entry on sync, shown in `status` and `ui` with relative last-sync times.
10. ✅ **Multi-machine project union.** Sync unions the freshly-built manifest with the existing one in staging, so every machine's projects are preserved (files already union via incremental sync); on restore each is re-slugged for the local machine.

## Remaining
11. ⬜ **Orphan history branch split.** Squash runs on `main` directly (bounded repo achieved); preserving config history separately is the refinement.
12. ✅ **Hooks auto-restore.** `clauderig pull` (the SessionStart hook) auto-restores when `autoRestore` is set AND the machine is fresh (no projects) — wires up a new computer on first session, never clobbers an established one. `config set-autorestore`.
13. ⬜ **Empirical Desktop-app resume check (Q4).** Rewrite is built + unit-proven; "does Cowork actually resume" needs driving the Electron app.
14. ✅ **Non-GitHub private remotes.** GitLab supported via `glab` (verified private), dispatched by host alongside GitHub/`gh`. Hosts with no verifiable privacy stay refused — the no-exceptions rule holds.
