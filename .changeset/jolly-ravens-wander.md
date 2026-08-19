---
"github.com/rigsmith/rigsmith": minor
---

clauderig: `status` now reports which account the machine-wide Claude Code CLI is logged in as — the email, the plan, and the alias if one is set. On a machine tracking several accounts the live login changes and is otherwise invisible until something fails, which is exactly the state status should surface. It also flags the two ways that identity can be quietly wrong: a credential and profile block that disagree (the desync `account doctor` exists to catch), and a clauderig active pointer naming a different account, which makes the arrow in `account list` a lie. A login clauderig has never captured is called out too, since `switch` cannot return to it.