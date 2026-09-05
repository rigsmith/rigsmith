---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack seed` names the seed by convention. A seed repository is not a project you clone and work in — it is the few kilobytes `rig stack init` rebuilds a whole stackspace from — and nothing in a list of repositories said so. The convention is `rigstack-<something>`: the interactive prompt now offers one derived from the stackspace's own directory (a stackspace in `acme-2.4/` offers `../rigstack-acme-2.4`, and an already-prefixed name is not doubled), and the help and docs use it throughout. Only a suggestion — the prompt is editable and the argument form takes whatever you type.