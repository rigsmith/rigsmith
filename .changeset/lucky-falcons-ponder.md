---
"github.com/rigsmith/rigsmith": patch
---

clauderig: `account` now states its scope — it switches the Claude Code CLI login only. Claude Desktop is a separate login with its own token store and its own claude.ai web session, and clauderig neither reads nor writes it, so a switch never signs Desktop in or out. `account list` says so, the `account` and `account switch` help say so, and docs/CLAUDERIG-ACCOUNTS.md records why moving Desktop's session is not something clauderig will do: Desktop signs in twice at moments a capture cannot see, Electron rewrites its config and holds its cookie DB open so writes underneath a running app are silently lost, and reading the session at all means driving a private Chromium sqlite schema.