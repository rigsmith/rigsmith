---
"github.com/rigsmith/rigsmith": minor
---

The doctor presentation layer is now `core/doctorui` instead of `internal/doctorui`, so code outside this module can render a `core/doctor` report and run the same fix-on-request flow — the pre-checked multi-select on a TTY, `--fix` to apply every fixable issue non-interactively. Nothing changes about how `rig`, `clauderig`, `changerig` or `shiprig` doctor behaves.