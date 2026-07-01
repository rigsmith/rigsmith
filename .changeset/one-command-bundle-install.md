---
"github.com/rigsmith/rigsmith": minor
---

Install the whole toolchain with one command, on every platform — no npm. A new combined `rigsmith` release archive (all four binaries) is exposed as `winget install RigSmith.Rigsmith`, `scoop install rigsmith`, `brew install --cask rigsmith/tap/rigsmith`, and `irm https://rigsmith.sh | iex` (the PowerShell counterpart to `curl https://rigsmith.sh | sh`). The per-tool packages still ship for single-tool installs. Also fixes the banner rendering as mojibake on legacy Windows consoles by switching the console output code page to UTF-8 at startup.
