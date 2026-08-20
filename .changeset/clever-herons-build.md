---
"github.com/rigsmith/rigsmith": patch
---

rig: `rig explain` now describes what the run path will actually do. A Go module whose mains live under `cmd/` no longer gets `go run .`, and the `--dry-run` suggestion places the flag before any `--` so it enables dry-run instead of being forwarded to your program.
