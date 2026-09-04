---
type: fix
"github.com/rigsmith/rigsmith"
---

clauderig: `sync` no longer re-commits a long session's whole transcript on every run. Past `retention.largeFileBytes` (8 MiB by default) a transcript is restaged only once it has grown by half that much again, or has gone quiet for half an hour — so an active marathon session costs one blob per chunk of new content instead of one per sync, and a finished session's last turn is still captured. Smaller transcripts sync on every change as before. One 47 MB session had put ~800 MB into a four-day-old sync repo this way.
