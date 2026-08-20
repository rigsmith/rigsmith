---
"github.com/rigsmith/rigsmith": patch
---

rig: two fixes for findings that were suppressed rather than surfaced in earlier reviews. `rig explain` asks the run path's own dispatch decision instead of always describing the root command, so a Go module whose mains live under cmd/ no longer gets `go run .` (a command the run path deliberately avoids), and its `--dry-run` suggestion puts the flag before any `--` so it enables dry-run instead of being forwarded.
