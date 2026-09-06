---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Stop warning about staging that is simply waiting for the next sync.

Loose changes in staging are what the time between syncs looks like: the live
tree keeps moving and the next scheduled sync takes what has accumulated. Amber
there meant the window sat at "needs attention" for most of every interval while
nothing was wrong — and a warning that is usually on is one nobody reads.

It stays green until the interval has passed twice without a sync landing, which
means the hook is not firing or a sync is not finishing. Both want a person.
