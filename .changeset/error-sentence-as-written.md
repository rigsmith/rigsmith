---
type: fix
"github.com/rigsmith/rigsmith"
---

An error at a terminal is shown as written. Its first word is no longer title-cased, which turned a flag or a package name into `--Only`, `--Changelog` or `App` (for a package named `app`), and the closing period is left off a headline that already ends in `?`, `!`, `.`, `:`, a closing code span or a quote, so `is it misspelled?.` and `published:.` are gone. The same goes for piped output, such as CI logs, which went through the same formatter.
