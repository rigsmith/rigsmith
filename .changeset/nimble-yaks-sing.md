---
"github.com/rigsmith/rigsmith": minor
---

clauderig: `clauderig sync` now backs up every Claude Desktop profile. A profile has the same shape as the machine-wide install, so it is handed to the engine as a root of its own (`desktop@<name>`) and gets the same walk, allowlist, retention, redaction and sidecar pruning — settings, chat history and clauderig's own record of the profile, never the login. Nothing is written inside a profile to make this work: the allowlist is include-only, so a profile contributes nothing the unprofiled Desktop root would not have, and the credential files are not in that set on any platform. `restore` recreates profiles on a machine that has never run `clauderig desktop` (the list comes from the repo, and locations from a $HOME-relative template), leaving each one signed out. Profile roots follow the Desktop root's enabled flag, and `status` and the dashboard list them.

Also excludes `local-agent-mode-sessions/skills-plugin` from the Desktop allowlist: it is the app's local copy of the bundled docx/pptx/xlsx/pdf skills, 446 of the 465 files (8.2 MB) in a real Desktop root, re-downloaded by Desktop on its own — and it would otherwise have been carried once per profile.
