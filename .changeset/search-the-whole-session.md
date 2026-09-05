---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

The window's search box searches the whole session, not just what a row shows.

It matched a row's own fields — title, last prompt, project, branch, id, client — and a separate **deep** toggle sent the term to the transcript bodies *instead*. So a word you remembered from a title and a word you remembered from the conversation needed different searches, and you had to know which kind of word you had before you could look for it.

One box now covers both. `sessions.Options` gains `Search`, which keeps a session when its row fields match **or** its transcript body does. The existing `Text` and `Content` remain the halves, and they still AND together — passing a word to both asks for sessions whose title *and* body contain it, which is not what typing a word into a search box means.

The cheap half runs first and a transcript is only opened for rows it missed, so a term that matches a title costs nothing extra. There is a test for exactly that, because it is the difference between a search that reads three files and one that reads seven hundred.

Rows found in the body report it the way `clauderig search` does — the hit count and the first snippet, in place of the project path, since the hit is why the row is there.

**Deep** survives, meaning body-only. That is still worth having when a common word matches half your titles.

Opening a session found that way leads with **where the term appears** — up to a dozen excerpts with the word marked, above the opening and closing prompts. The prompts say what a session was *for*; when you arrived from a search, the excerpts say why it came back, which is the thing you clicked to find out. `Detail` takes the term for this, so the pane titles itself with what was searched rather than the frontend restating what it asked.

Excerpts are built as text nodes, not markup. The text is somebody's conversation, and putting it into the page as HTML would run whatever they happened to have written down.

And searching now says it is working. Opening transcripts is not instant, and a list that sits there unchanged reads as a search that found nothing — the one answer it must not give by accident. The indicator waits 150ms before appearing, so a fast search never flashes it, and it only shows when there is a term: an unfiltered reload is quick, and announcing every background poll would be noise.
