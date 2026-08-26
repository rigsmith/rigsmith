---
type: build
"github.com/rigsmith/rigsmith"
---

Dependencies updated and the minimum Go toolchain raised to 1.26.7, clearing every `govulncheck` finding. Six were in the standard library, where no dependency bump can reach them — building from source now uses that toolchain, which Go switches to on its own. CI scans for known vulnerabilities on every change from here.
