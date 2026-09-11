---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Bringing one Claude Desktop window to the front now reaches the window you asked for.

Two bugs, stacked. The scan that found a profile's processes matched the `--user-data-dir` flag anywhere in a command line, and every Electron helper inherits it — one profile answered with its main process and a dozen renderers and utilities, and the raise went to whichever the scan happened to list first. A helper has no windows; raising one is a no-op or an error depending on how you ask.

Underneath that, the raise itself used System Events to set a process frontmost, which does not work for a second instance of one application: raising the first Claude instance worked and raising the second did nothing at all, silently, with both holding a visible window. Activation is per-application, and two processes sharing a bundle cannot be told apart that way.

Raising now goes through `NSRunningApplication`, which takes a process id — reached through JXA, since these binaries build without cgo. It needs no Automation or Accessibility grant, because nothing sends an Apple Event to another application, and a pid that is not an application (every helper) is refused rather than silently accepted.

`desktop open <profile>` gained the same precision: it used to activate the application, so with two profiles open it could put the wrong window in front and report success.