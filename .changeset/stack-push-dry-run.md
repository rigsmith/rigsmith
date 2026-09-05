---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

`stack push --dry-run` is dry. The global `--dry-run` flag is accepted by every verb, and the stack verbs ignored it — so `rig stack push <repo> --dry-run` did the real push, fast-forwarding a repository you own with history you had asked only to see, and reported `pushed …` with the remote already moved. Now it runs the local filter, prints `would push <repo> to <upstream>:<branch>` and the commits that would go, one per line, and stops: nothing reaches the remote, and nothing local records a push — no take-back merge, no cursor move. `stack propose --dry-run` stops the same way, saying which commit it would push to which branch of your fork, without touching the fork, the `refs/rigsmith/propose/<repo>` ref, or the manifest's memory of the branch. No other stack verb writes to a remote.
