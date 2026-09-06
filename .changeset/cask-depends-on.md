---
type: fix
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

The Homebrew cask no longer warns on install.

It declared its minimum macOS with the string form Homebrew has deprecated, so
every `brew install --cask clauderig-ui` printed a deprecation notice and asked
the user to report it. Same requirement, written the way Homebrew now wants.
