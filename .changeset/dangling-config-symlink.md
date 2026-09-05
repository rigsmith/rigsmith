---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

a config file that is a dangling symlink — `rig.stack.jsonc`, `.rig.json`, `.changeset/release.jsonc`, any file the tools find by probing — is an error naming the file, not "no config here". Reading it followed the link, found nothing, and fell back to defaults; for a stack manifest that meant a release would stamp every member as its own. The stack manifest's member keys are now also checked on read by the same rule `rig stack` applies on write, so a hand-edited `"./lib"` is refused rather than matching nothing.
