---
type: fix
scope: codexrig
"github.com/rigsmith/rigsmith"
---

Security: restrict hook trust to exact current codexrig command/event pairs in the explicitly requested scope. Refuse marker lookalikes and outdated commands, prevent global/project trust leaks, and show every approval and refusal in text and JSON. Doctor's global trust fix uses the same restrictions; reinstall outdated hooks before trusting them.

Leave unrelated hooks alone without doctor warnings. Report stale installations as out of date with a reinstall repair, and refuse hooks with missing source paths with an instruction to update Codex.

Run `codexrig doctor` after upgrading: old substring-based approval may still allow a lookalike to run. STILL TRUSTED hooks fail doctor and trust and include manual revocation steps; this release does not automatically revoke them. Scope now binds both the file and the exact sync/guard plans, matcher and full fields. Unknown fields such as commandWindows and escaping project symlinks fail closed. Reports escape terminal controls, retain all partial outcomes and fail on any in-scope refusal. Safe reinstall preserves unrelated handlers and suppresses no-op fixes; missing sync hooks still warn.
