---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

`rig stack send` is now `rig stack propose`. `send` keeps working as an alias, so nothing you have written breaks.

Both verbs took work out of a stackspace and neither name said where it went, which left a pause every time: `push` fast-forwards a repository you own with its history, `send` put one squashed commit on a branch of your fork for somebody else to accept. "Propose" names the part that is actually distinctive — you are offering a change to a project that is not yours, and someone has to take it. Push to yours, propose to theirs, which is a sentence people get right without being taught it.

It stays forge-neutral, which is why it is not `pr`.

`propose` also asks for anything you leave out. Running it with no arguments, or with a repo and no branch name, opens the picker that until now only `rig ui` could reach — the same form, from the verb itself. Give both arguments and nothing prompts; run it without a terminal and it says which argument is missing rather than waiting for an answer nobody is there to give.
