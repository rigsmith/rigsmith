---
type: fix
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

The sessions window no longer hangs on Windows when a CLI store's project folder has to be named from its transcripts.

Recovering the real directory behind a project slug walks up the working directory a transcript recorded, and it stopped when it saw `/`. Windows spells its root `\` or `C:\`, `filepath.Dir` returns those unchanged, and the walk ran forever — the window wedged on the first folder whose name had to be recovered. It now stops where `Dir` stops changing the path, which is the root on every platform.

A group id from the window that begins with `/` is also refused on Windows now. The guard called `filepath.IsAbs`, which is the host's rule: `/etc/passwd` is not absolute on Windows because it names no volume, so an id written to be refused walked straight past it.