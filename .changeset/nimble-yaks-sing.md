---
"github.com/rigsmith/rigsmith": minor
---

clauderig: `clauderig sync` now backs up your Claude Desktop profiles — settings and chat history, never the login — staged as `desktop@<name>` with the same retention and redaction as the machine-wide install. `restore` recreates them on a machine that has never run `clauderig desktop`, each one signed out. Profiles follow the Desktop root's enabled flag, and `status` lists them. Removes `clauderig desktop share`, which pooled history between profiles by writing inside them.

Also stops syncing `local-agent-mode-sessions/skills-plugin` — Desktop's own copy of the bundled docx/pptx/xlsx/pdf skills, 8 MB per profile that the app re-downloads on its own. Your next sync will drop it from the repo.
