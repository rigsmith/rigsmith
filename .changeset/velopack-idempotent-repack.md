---
"github.com/rigsmith/rigsmith": patch
---

Velopack: re-packing the same version is now idempotent. The adapter clears that version+channel's existing nupkg(s) before `vpk pack` (prior versions stay, so delta generation still works), so vpk no longer fails with "a release equal or greater already exists" — `shiprig release --from build` resumes cleanly after a partial failure and local re-runs no longer need a manual `dist/releases` cleanup.
