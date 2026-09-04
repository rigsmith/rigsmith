---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---
`sync` no longer re-commits a long session's whole transcript on every run. Past `retention.largeFileBytes` (8 MiB by default) a transcript is restaged only once it has grown by half that much again, or has gone quiet for half an hour — so an active marathon session costs one blob per chunk of new content instead of one per sync, and a finished session's last turn is still captured. Smaller transcripts sync on every change as before. A new SessionEnd hook runs `sync --flush`, which restages that session's transcript past the throttle, so a finished session's last turn is captured at once without disturbing other sessions' throttling (`clauderig doctor --fix` installs it on an existing setup). One 47 MB session had put ~800 MB into a four-day-old sync repo this way.
