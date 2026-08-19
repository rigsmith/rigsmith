# Rewind notes — clauderig account switching (2026-08-19)

main was at `6dceb17`; rewound to `6648432` (PR #200). Everything below is
preserved in the local tag `archive/desktop-account-switching`.

## The four commits removed

### 1. `28db708` — feat(clauderig): switch Claude Desktop accounts without a re-login (#201)
+1360 / -6. New `account/desktop.go`, `desktop_darwin.go`, `desktop_other.go`,
`desktop_test.go`; +286 in `commands/account.go`; +16 in `tui/account.go`;
+6 in `sessionstore.go` (the `Desktop bool` status field).
Snapshotted Desktop's two auth surfaces — the OAuth token cache in `config.json`
and the claude.ai cookies — and restored them on switch. Cookies were exported
and re-imported by shelling out to `sqlite3` against Desktop's `Cookies` DB.
**Verdict: DROP. Nothing CLI-relevant in it — every hunk is Desktop-specific.**

### 2. `25417f6` — fix(clauderig): stop `switch` writing a blanked credential over the live login (#202)
Three independent CLI fixes, none of which touch Desktop:
- `Store.mergeCredential` — carries forward top-level fields (`organizationUuid`,
  `mcpOAuth`) that a narrower source doesn't carry, so `add --from-session`
  and switch round-trips stop stripping them. Losing `organizationUuid` made
  `doctor` report "both halves agree" vacuously.
- `RunningInstances` now also scans the process table. Claude Code 2.1.227 writes
  neither session registry, so the guard saw "nothing running" with a live
  session in the room — a refusing guard that silently permits. `account run`
  profiles are excluded by comparing their `CLAUDE_CONFIG_DIR`.
- `HasTokens` guard in `doSwitch` — refuses to switch INTO a stored credential
  whose tokens were blanked, which would just log the machine out.
**Verdict: KEEP. Re-applied cleanly on the rewound main.**

### 3. `6dceb17` — fix(clauderig): refuse a Desktop snapshot that has tokens but no web session
+157 / -4 across `desktop.go`, `desktop_test.go`, `commands/account.go`.
Diagnosed the reported symptom (switch logs Desktop out, CLI keeps working):
Desktop signs in twice — OAuth writes `config.json`, the webview authenticates
claude.ai separately with a `sessionKey` cookie — so a capture between those two
moments stores tokens with no web session. Added cookie-name recording and a
`Complete()` check refusing such a snapshot both on capture and on restore.
**Verdict: DROP with the feature. The CLI-side hunk is one `desktopPartial`
capture note; it has no meaning without Desktop capture.**

### 4. `f3fd875` — docs(clauderig): mark the Desktop design shipped, record the Windows follow-up
Adds `docs/CLAUDERIG-DESKTOP-DESIGN.md` (370 lines).
**Verdict: DROP. Rationale for the withdrawal moves into
`docs/CLAUDERIG-ACCOUNTS.md` so the next person doesn't re-attempt it blind.**

## Why Desktop switching is being withdrawn

- Desktop's auth is spread over two stores that sign in at different moments, so
  a capture is only valid in a window the user cannot see. That's what produced
  the original bug report.
- Electron holds the `Cookies` SQLite DB open and rewrites `config.json` on exit,
  so any write underneath a running app is silently clobbered. The mitigation was
  a guard refusing to switch while Desktop runs — which means the feature works
  only when the app the user wants switched is closed.
- Moving cookies at all requires shelling out to `sqlite3` against a private
  Chromium schema that Anthropic can change in any release.
- `switch --dry-run` never consulted the Desktop guard, so it reported
  "no live sessions — switch would proceed" for a switch that would be refused.

Account switching is now Claude Code (CLI) only, and says so.
