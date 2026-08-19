---
"github.com/rigsmith/rigsmith": patch
---

clauderig: `desktop open` and `desktop quit` now resolve their target the same way — the profile you named, else the one bound to this directory, else a picker on a terminal (`quit` lists only the open windows), else an error naming both ways to say which. Neither picks for you. `quit` takes an optional name to match `open`, and reports "no profile windows are open" as a fact rather than an error.