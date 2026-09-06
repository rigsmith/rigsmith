---
type: feat
scope: clauderig-ui
"github.com/rigsmith/rigsmith/ui"
---

Read a session's conversation from the panel, both sides, with a search box.

The detail showed a session's opening and closing prompts with a count of what
sat between them and no way to reach it. That gap now offers to open the
conversation out in place — the two ends are the context for the middle.

It shows both sides. Reading back a conversation with one voice removed is not a
shorter conversation, it is a different and confusing document, so the assistant
turns come too: prompts sit to the right, answers to the left, each coloured for
its side. That is as much chat window as this is trying to be.

A search box over the conversation filters it to the turns that mention
something and marks where, because by that point you are inside one session
looking for a place in it, not looking for a session.
