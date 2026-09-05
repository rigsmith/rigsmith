---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

The session listing no longer hides sessions, and deleting one no longer reports success it did not achieve.

Transcripts whose file timestamp fell outside the window were excluded without being read, on the reasoning that a timestamp moves forward and never back. That does not hold here: a restore rewrites whole trees and stamps them with the time it ran — one machine's tree had 541 transcripts inside the same minute — and the session hidden that way is the one you opened the listing to find. Every transcript in scope is now read; it was already a bounded read of the file's tail.

`clauderig sessions delete` discarded the error from removing a session's sub-agent transcripts and reported the store fully cleared, leaving conversations on disk under a session the listing said was gone. It now reports the failure, and retries the directory even when the transcript itself is already absent — the state a previous half-failed delete leaves behind. It also refuses when it cannot determine whether the session is running, rather than reading a check that did not complete as "nothing is running".

`clauderig reroot` accepts a session id in any casing, and rewrites the record's own `cwd` rather than the first path-shaped value in it — a record carries several, and rewriting the wrong one left the session filed exactly where it was.

`clauderig search --json` now refuses `--raw` and `--all` instead of quietly emitting grep output to a caller expecting a document, and a session known only from the ledger keeps its project directory in that document, so the `resume` command it hands you does not run wherever you happen to be standing.
