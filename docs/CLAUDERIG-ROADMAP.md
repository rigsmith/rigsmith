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

## Polish / UX
5. ⬜ **Distribution.** goreleaser entry + install script + README; wire `curl|sh` / brew / scoop.
6. ⬜ **Desktop `config.json` preferences.** Currently excluded wholesale (volatile); add a keep-only filter to sync just `preferences`.
7. ⬜ **Conflict resolution.** Pull is ff-only; on divergence it errors. Build the per-file picker / `git mergetool` handoff.
8. ⬜ **Richer TUIs.** `ui` dashboard is read-only + dispatch; add the restore-preview screen and interactive restore-safety prompt.
9. ⬜ **Device registry.** Track per-device last-sync timestamps for the dashboard.
10. ⬜ **Multi-machine project union.** Manifest is rebuilt from the local machine each sync (reflects last pusher only); merge projects across devices.

## Deferred / v2
11. ⬜ **Orphan history branch split.** Squash runs on `main` directly (bounded repo achieved); preserving config history separately is the refinement.
12. ⬜ **Hooks auto-restore.** `SessionStart` only pulls (updates staging); a new machine still needs a manual `restore` (intentionally safe).
13. ⬜ **Empirical Desktop-app resume check (Q4).** Rewrite is built + unit-proven; "does Cowork actually resume" needs driving the Electron app.
14. ⬜ **Non-GitHub private remotes.** GitLab/self-hosted refused by the hard private gate; v2 if wanted.
