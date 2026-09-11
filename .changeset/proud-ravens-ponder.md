---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`clauderig account prepare <account>` readies an account's session profile and prints its `CLAUDE_CONFIG_DIR`, for programs that launch `claude` themselves. It does everything `run` does short of starting Claude Code and never touches your machine-wide login. `--json` returns one object, and on refusal a stable `reason` a launcher can branch on: `no-such-account`, `ambiguous-account`, `unmapped-directory`, `no-tokens`, `session-unknown`, `profile-desync` (the profile was re-logged as a different account inside a session), or `failed`. A success always reports `session: ok`.

Two resolution fixes that reach every `account` command: an exact email stored for two organizations is now refused as ambiguous with the ids to use, instead of silently picking one; and a directory mapped to an account that no longer exists is reported as such, instead of as "not mapped".
