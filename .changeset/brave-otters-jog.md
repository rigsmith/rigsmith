---
type: feat
---

`rig alias` installs short shell aliases for the verbs you type most — `rr` → `rig run`, `ri` → `rig install`, `rup` → `rig upgrade`, `rrm` → `rig uninstall`. They're written to your shell startup file (zsh, bash, fish, or PowerShell) in their own marked block, separate from `rig setup`'s completion/cd block, so the two are managed independently: `rig alias install` / `rig alias remove` splice idempotently and never touch your setup block, and vice versa. Aliases are opt-in (they claim names in your shell's namespace), and the uninstall alias is deliberately `rrm` rather than `run` so it can't shadow the ubiquitous `run` command. `rig alias list` shows the set; `--print` inspects the snippet without writing.
