---
type: fix
scope: codexrig
"github.com/rigsmith/rigsmith"
---

Security: restrict hook trust to exact current codexrig command/event pairs in the explicitly requested scope. Refuse marker lookalikes and outdated commands, prevent global/project trust leaks, and show every approval and refusal in text and JSON. Doctor's global trust fix uses the same restrictions; reinstall outdated hooks before trusting them.

Leave unrelated hooks alone without doctor warnings. Report stale installations as out of date with a reinstall repair, and refuse hooks with missing source paths with an instruction to update Codex.
