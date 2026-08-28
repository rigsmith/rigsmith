---
type: fix
"github.com/rigsmith/rigsmith"
---

`curl -fsSL https://rigsmith.sh/changerig | sh` now installs changerig instead of bouncing you to the docs. The install edge function kept a tool allowlist from the days before changerig had a release archive, so a request for it fell through to a 302 — even though both installer scripts have accepted `changerig` by name for a while, and every other channel (winget, Homebrew, the combined archive) has shipped it all along. changerig is on the allowlist now, and a browser hitting the same path lands on `/changerig/` rather than the site root.
