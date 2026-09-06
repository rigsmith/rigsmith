---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Read the whole conversation from the session panel.

The detail showed a session's opening turns and its closing ones with a count of
what sat between them — "⋯ 5 more prompt(s) ⋯" — and no way to see it. That gap
is the one place in the panel where the answer is "read the rest", so it now
offers to, and opens the conversation out in place: the two ends are the context
for the middle, and losing them to read it is how you end up scrolling back to
work out where you are.

Fetched only when asked, since the summary does not need it, and capped — a
conversation longer than the window will read says so rather than letting its
last shown turn read as the last turn there was.
