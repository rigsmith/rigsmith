---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack` can now fuse a private upstream. Importing or pulling one previously failed with a `401`, because the engine is what fetches upstream and rig gave it nothing to authenticate with. rig now asks git for the credential you already hold for that host — the keychain, the GitHub CLI's helper, whatever `git credential fill` answers — and passes it to the engine for that single fetch. There is nothing to configure and nothing new stored: if you can clone the repo, you can fuse it. Public upstreams are unaffected, and `send` never needed this, since it pushes to your fork with plain git. The credential travels in the environment rather than on the command line, so it stays out of process listings and out of the error text when a fetch fails.
