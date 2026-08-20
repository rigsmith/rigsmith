---
"github.com/rigsmith/rigsmith": patch
---

clauderig: `status` and `doctor` now tell you whether your sync actually reached the remote. `last sync` reports the last local commit, so it stays green while pushes are being rejected — `status` adds an `unpushed` line and `doctor` a `pushed` check that fails when commits have never left the machine, pointing at the reconcile path when the remote has diverged.
