---
"github.com/rigsmith/rigsmith": minor
---

rig custom commands now run cross-platform by default. A `.rig.json` shell-string command (e.g. `"lint": "eslint . && prettier --check ."`) executes through an in-process portable shell — pipes, `&&`/`||`, `$VAR`, globbing, and `cp/mv/rm/mkdir` all behave identically on Linux, macOS, and Windows, so per-OS `os.{macos,windows,linux}` variants are no longer needed just to be portable.

Opt back into the OS shell with `"shell": "system"` (config-level, or per command) for scripts that need a real userland (`sed`, `awk`) or OS-specific syntax. Argv-form commands are unaffected (still exec'd directly).

Behavior change: existing shell-string commands switch from `/bin/sh -c` / `cmd.exe /c` to the portable shell. The output and exit code are unchanged; only the interpreter differs. Set `"shell": "system"` if a command relied on a host-shell feature the portable shell doesn't provide.
