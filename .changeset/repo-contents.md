---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

`clauderig repo` now says what the repo is made of, not just how big it is.

A total says the repo is large without saying what it is large *with*, and the two have entirely different remedies: transcripts answer to retention, attachments to the allowlist, backups to not keeping them, history to a prune. Reading "1.62 GB" and reaching for a prune is the obvious move and, on this repo, the wrong one — 97% of the checkout is conversation, which no amount of squashing touches.

```
  what it holds
    transcripts                   1.58 GB   97%  1112 files
    attachments & tool output     28.9 MB    2%  1073 files
    plugins                        5.5 MB    0%  461 files
    Desktop session index          4.7 MB    0%  93 files
    memory                         1.7 MB    0%  501 files
    skills, commands & agents      922 KB    0%  59 files
    clauderig records              558 KB    0%  6 files
    Desktop config                 504 KB    0%  82 files
    transcript backups             118 KB    0%  1 file
```

Anything under 2% of the checkout folds into a single `other` row, kept last whatever its size, naming its three largest members and counting the rest. A tail of rows all reading 0% buries the one line worth acting on under its own precision. It only folds when at least two categories qualify — replacing one named row with an `other` containing exactly it loses the name and gains nothing.

Categories are ordered so the specific cases sit above the general ones that would otherwise swallow them: a `.pre-import` backup is a `.jsonl` too, and memory lives under `projects/` exactly like a transcript does. Both would land in "transcripts" under a naïve rule, which is the sort of thing that makes a breakdown quietly wrong rather than visibly broken.

The walk reads metadata only, so it costs a stat per file rather than a byte of I/O per byte stored, and `.git` is skipped: history is reported separately and is not part of what the repo is keeping.
