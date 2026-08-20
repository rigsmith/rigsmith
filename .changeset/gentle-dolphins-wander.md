---
"github.com/rigsmith/rigsmith": minor
---

clauderig: `sync` and `pull` now settle a diverged sync repo by themselves instead of stopping at the first conflict. Conflicts are resolved by policy — the clauderig manifest and device registry merge across machines, transcripts and memory notes keep both machines' additions, and machine-local state takes the newer snapshot — so a sync started by a hook or an agent no longer needs a terminal to finish. A staging repo left part-way through a merge by an earlier run is now repaired automatically instead of failing every session start with "unmerged files", and `clauderig doctor` reports that state as a `staging repo` failure it can fix.