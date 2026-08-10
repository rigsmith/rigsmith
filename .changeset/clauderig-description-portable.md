---
type: fix
"github.com/rigsmith/rigsmith"
---

`clauderig`'s description no longer makes Windows tooling read the binary as an installer.

komac decides whether a `.exe` inside a zip is an installer or a portable binary by substring-matching its PE `FileDescription` and `OriginalFilename` against `["installer", "setup", "7zs.sfx", "7zsd.sfx"]`. Nothing else about the binary is considered. clauderig's description read *"Sync your Claude Code **setup** across machines"*, so komac wrote `NestedInstallerType: exe`, winget unpacked the zip and put nothing on PATH, and the failure surfaced only as a moderator asking "Is this a Portable package?" — 23 days after the ClaudeRig 1.4.0 submission, and again on 1.5.1.

It was never a quirk of the binary. `rig`, `shiprig` and `changerig` were always detected correctly, and `rig` actually contains *more* installer-related strings than clauderig; the only difference was one word in a description. Reworded to "configuration", and verified by rebuilding the Windows binary and re-running the analyzer: `NestedInstallerType: portable`.

A test in `build/winres/` now fails if any tool's `FileDescription` or `OriginalFilename` picks up one of those words. It is deliberately stricter than komac, which matches case-sensitively — a capitalised "Setup" would slip past komac today, but it reads as an installer to a human and to any future tightening of that check. The correction step in `winget-submit.sh` stays as a regression detector: it now warns loudly rather than quietly fixing, because after this it should never fire.
