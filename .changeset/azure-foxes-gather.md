---
"github.com/rigsmith/rigsmith": minor
---

rig: new `rig stack` — repos you maintain forks of, fused into one git history so a change can span them in a single commit, while each still leaves as an ordinary pull request to its own upstream. `stack init` imports the repos your `rig.stack.jsonc` names, `stack pull` takes upstream's new commits into the right directory, and `stack send <repo> <name>` puts that project's changes on your fork as a branch holding one clean commit — no trace of the other projects. Send again to the same branch to update an open PR. Guide: https://rigsmith.dev/rig/stack
