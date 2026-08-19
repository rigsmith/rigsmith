---
"github.com/rigsmith/rigsmith": patch
---

clauderig: stop syncing Cowork sandbox contents from the Desktop root. A `local_<id>/` directory under `local-agent-mode-sessions` is a session working directory — audit log, build outputs, an `.audit-key`, and the documents the user uploaded to that session — and was being carried to the sync remote wholesale. Only the `local_<id>.json` sidecar (the session metadata) syncs now. Also fixes the Desktop `config.json` keep-filter, which retained a `preferences` key the app no longer writes and so synced an empty document; it now keeps `locale` and `userThemeMode` as well.
