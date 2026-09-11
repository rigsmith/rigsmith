---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

The Homebrew casks no longer strip the macOS quarantine attribute, which also ends the deprecation warning every `brew upgrade` printed.

Each cask ran `xattr -dr com.apple.quarantine` over the staged binary after install, from the days when releases were unsigned and Gatekeeper would refuse to run them. They are Developer ID signed and notarized now, so a quarantined binary passes on its own — checked by quarantining a released 1.18.0 binary by hand and running it.

That makes the hook a check being disabled on every user's machine for a check that now passes. Homebrew had independently deprecated the `postflight` stanza GoReleaser emits for these hooks, so each upgrade printed a warning naming our tap; GoReleaser has no way to emit the replacement, its cask hooks being pre/post install/uninstall and nothing else. Removing the hook settles both.

If a release ever ships unsigned, cask installs will now be blocked rather than quietly working. That is the correct failure: the answer is to sign the release, not to turn Gatekeeper off for everyone who installs it.