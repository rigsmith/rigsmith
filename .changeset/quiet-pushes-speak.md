---
type: fix
"github.com/rigsmith/rigsmith"
---

A failing `dotnet` command now reports what went wrong instead of `exit status 1:` and nothing.

The .NET adapter captured both streams and built its error from stderr alone. The dotnet CLI writes its diagnostics to **stdout** — a rejected push says `warn : No API Key was provided` and `error: Response status code does not indicate success: 403 (Forbidden).` there, with stderr empty — so the one thing that explained the failure was captured and discarded, and a 403 surfaced as an exit code, a colon, and blankness. Reproducing the command by hand was the only way to see the reason.

Errors now carry stderr plus the tail of stdout, bounded to the last 20 lines so `dotnet pack`'s build log can't bury the summary it ends with, and a silent failure reads as `(no output)` rather than trailing off. This matches the treatment the velopack adapter already applies to `vpk`, which writes its fatal line to stdout for the same reason.

Tests cover each stream in isolation, both together, the silent case, the bounding, and the end-to-end wiring through `runCmd` — the last using a sentinel split across `printf`'s format and argument so it exists only in the command's output and never in the argv the error echoes back. (Asserted the obvious way, that test passes with the fix backed out.)
