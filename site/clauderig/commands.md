# Commands

| Command | What |
|---|---|
| `init` | First-run wizard: remote (private), machine identity, roots, hooks |
| `sync` | Walk → redact → manifest → tripwire → commit → push (`--dry-run`) |
| `pull` | Fetch latest into the staging repo (no write to `~/.claude`) |
| `restore` | Restore here, rewriting paths (`--dir`, `--backup`, `--force`, `--prune`) |
| `status` | Sync state: remote, last sync, roots, hooks |
| `hooks` | `install` / `uninstall` / `status` the Claude Code hooks |
| `config` | `show` / `set-remote` / `set-prune` |
| `doctor` | Preview path resolution + roots for this machine |
| `ui` | Interactive dashboard |

## The sync → restore loop

`sync` snapshots your `~/.claude` setup, redacts secret-bearing fields, rewrites
machine-specific paths into a portable form, and commits/pushes to your private
repo. `restore` does the inverse on another machine: it pulls, rewrites the
portable paths into this OS's slugs, and merges — keeping any local secrets in
place so a new machine simply re-authenticates.

## Hooks

```sh
clauderig hooks install
```

Wires two Claude Code hooks: **SessionStart → pull** (so each session starts
from the latest synced state) and **Stop → sync** (so your work is captured when
a session ends). Both are portable across OSes and idempotent.

::: tip
See the [design doc](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/CLAUDERIG-DESIGN.md)
and [roadmap](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/CLAUDERIG-ROADMAP.md)
for the full picture.
:::
