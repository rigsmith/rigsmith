---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`clauderig account prepare <account>` readies an account's session profile and prints its `CLAUDE_CONFIG_DIR`, for programs that launch `claude` themselves. It does everything `run` does short of starting Claude Code and never touches your machine-wide login. `--json` returns one object with a stable `reason` code on refusal (`no-such-account`, `unmapped-directory`, `no-tokens`, `session-unknown`), so a launcher can explain why it could not use an account instead of spawning into a profile that was never seeded.