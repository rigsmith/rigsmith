---
type: fix
"github.com/rigsmith/rigsmith"
---

Installing rigsmith now verifies what it downloads. Every release publishes a checksums file covering the installer and every archive, signed with Sigstore by the release workflow itself (no key to manage). The `curl | sh` installer, the PowerShell installer and `self-update` download everything first and check each file against it, and check its signature, before installing anything. A mismatch, or a missing or invalid signature, installs nothing. Checking the signature needs `cosign`; without it the checksums are still checked and the installer says so, and `RIGSMITH_REQUIRE_SIGNATURE=1` refuses instead. `rigsmith.sh` now serves the installers from the latest release rather than from the main branch, so the script always matches the release it installs.