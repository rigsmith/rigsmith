---
"github.com/rigsmith/rigsmith": patch
---

The interactive `rig run` picker gains a `d` key that sets the highlighted project as the repo's `defaultProject` (so a bare `rig run` launches it without the picker), or clears it when pressed on the project that already is the default. The current default is marked with a green "★ default" tag in the list.
