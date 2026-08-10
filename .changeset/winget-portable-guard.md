---
type: fix
"github.com/rigsmith/rigsmith"
---

The release workflow now verifies its own winget manifests, and a new script answers a moderator's questions before they're asked.

`RigSmith.ClaudeRig` 1.4.0 took 23 days to merge because its manifest wasn't `NestedInstallerType: portable`: winget unpacked the zip and put nothing on PATH, automatic validation reported it as an unattended-install timeout (which reads like flakiness), and it took a human asking "Is this a Portable package?" to name it. Every pipeline we own was green the whole time.

`scripts/check-winget-manifests.sh` runs after GoReleaser and fails the job unless every generated manifest is portable with a command alias per binary. GoReleaser opens the PRs before the check can run, so this is an alarm rather than a gate — but it fires in minutes instead of weeks, while the submission is still fresh enough to fix quietly.

`scripts/winget-note.sh <version>` posts a short note on each open submission — portable, what lands on PATH, publisher, and that the binaries are signed. It previews by default (it comments on a third-party repo), skips PRs it has already noted, and exists because our submissions keep drawing `Policy-Test-1.2` manual review: ChangeRig 1.4.0 needed six waiver rounds.

`docs/WINGET-SUBMISSIONS.md` records what the 1.4.0 batch actually cost and why — including that two of the five merged the same day, so winget is not uniformly slow for us — and the evidence against the obvious-but-wrong theory that the word "Claude" is what draws manual review (`RigSmith.ClaudeRig` has never tripped it; ChangeRig, with no brand word at all, tripped it twice).
