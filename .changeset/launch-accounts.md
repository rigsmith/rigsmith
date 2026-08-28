---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

The Accounts panel can now start Claude Code or Claude Desktop as any account.

Each row gains a terminal button and, where a Desktop profile is bound to that account, a window button. The terminal one runs `clauderig account run <id>` — which scopes the credential to that one terminal rather than changing the machine-wide login — so starting a second account does not sign the first out, and the live-session guard never enters into it. The Desktop one runs `clauderig desktop open <profile>`, which focuses an existing window rather than launching a second.

That distinction is the point: **launching is not switching**. The Switch button still changes the machine-wide login and is still refused while Claude Code is running, because that operation genuinely cannot be done underneath a live session. Neither launch button is guarded, because neither needs to be — the whole reason `account run` and Desktop profiles exist is that they keep accounts apart without anything being swapped.

Which profile belongs to which account comes from the binding recorded when the profile was created, and whether it is already open is a process check. Both are best-effort: Desktop is a separate application with its own login, a machine with no profiles is the ordinary case, and a failure to read either costs the buttons rather than the accounts list they sit beside. An unreadable process scan is treated as "closed" — the button then says "open", and opening an already-open profile focuses it, so guessing that way is harmless.
