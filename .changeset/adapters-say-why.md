---
type: fix
"github.com/rigsmith/rigsmith"
---

Every ecosystem adapter now reports what its tool actually said when a command fails, whichever stream the tool chose.

The .NET fix — errors built from stderr alone lose the diagnosis whenever a tool writes it to stdout — was not specific to .NET. `node`, `cargo`, `gomod`, `tauri` and `electron` all wrapped `strings.TrimSpace(stderr)` the same way, so any of them could reduce a real failure to an exit code and a colon. Meanwhile `velopack` had already hit this with `vpk` and fixed it locally, with a comment describing the identical `exit status 255:` symptom.

That treatment is now one shared helper, `core/cmderr.Detail`: stderr plus the tail of stdout (bounded to 20 lines, so a build log can't bury the summary it ends with), or `(no output)` when a command fails silently. All seven adapters use it, and the two private copies are gone.

Two nearby callers were deliberately left alone, because the pattern is only a bug where stdout carries diagnostics:

- `core/auth`'s 1Password path — `op read`'s stdout is *the secret*. Appending it to an error would write a credential into a message that gets logged.
- `core/plugin`'s subprocess host — stdout is the JSON protocol channel, not human output, and the plugin contract puts errors on stderr. It already falls back to the bare error rather than printing a dangling colon.

The behaviour is pinned by tests on the shared helper (each stream alone, both together, the silent case, the line bound) plus the adapter-level wiring test.
