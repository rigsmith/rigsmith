---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`clauderig desktop shortcut <name>` makes a clickable launcher for a Desktop
profile — a `.app` bundle on macOS, a `.lnk` on Windows — on the desktop
(`--to desktop`) or in `~/Applications` / the Start Menu (`--to apps`), with
`--all` for every profile and `--rm` to take them away again. `desktop add`
offers one at the end on a terminal (`--shortcut` / `--no-shortcut` to decide
without being asked), and `desktop rm` deletes a profile's shortcuts along with
it so no icon is left opening nothing.

The shortcut runs `clauderig desktop open <name>` rather than launching Claude
with the profile flag, so clicking it twice focuses the open window instead of
starting a second instance on the same profile.
