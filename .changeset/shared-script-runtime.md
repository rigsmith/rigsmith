---
"github.com/rigsmith/rigsmith": patch
---

Extract the Tengo scripting runtime and the cross-platform portable shell into shared `core/script` and `core/shellrun` packages (previously private to shiprig's release pipeline), so other tools can reuse them. No behavior change for shiprig releases.
