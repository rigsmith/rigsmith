---
type: refactor
"github.com/rigsmith/rigsmith"
---

The doctor presentation layer moved to `core/doctorui`, so code outside this module can render a `core/doctor` report and run the same fix-on-request flow. Nothing changes about how `rig`, `clauderig`, `changerig` or `shiprig` doctor behaves.
