---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

When Open in Desktop is refused because another Desktop window is open, the window now offers to send it anyway.

The refusal is right: a deep link is routed by *scheme*, not to a particular window, so with more than one Desktop window open the OS decides which receives the session — and picking wrong files somebody's conversation under the wrong account. The CLI has always had `--anyway` for the case where you know which window is in front and just want it sent. The window had no way to say so, which left you reading an error explaining a flag you could not reach.

A **Send anyway** button now appears under that particular error, in both the session detail and the full window's drawer, and only after the guarded attempt has actually been refused.

The error itself is scrolled into view when it appears. These messages run to several lines and land at the very bottom of a pane you have usually scrolled, so unscrolled the text is clipped mid-sentence and anything beneath it — including the button — is simply not on screen.

It is deliberately not labelled "open it in the window that's already open". That is the usual outcome and almost certainly what you want, but the mechanism cannot promise it — the OS chooses, and a button that claims otherwise would be making a guarantee out of a likelihood. The tooltip says exactly that. It also does not offer itself twice: once the override has been used, a second identical attempt has nothing new to try.
