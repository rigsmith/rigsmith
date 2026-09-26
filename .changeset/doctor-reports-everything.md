---
"github.com/rigsmith/rigsmith": patch
---

**changerig:** `doctor` reports every problem it finds. A manifest edited by hand no longer hides recorded releases that were never tagged: each gets its own `release record` row. A changeset that can't be parsed, which stops `status` and `version`, is now a failing `changeset files` check naming the file and the parse error, where doctor used to skip the pending changesets and report all good. The other checks still run on the changesets that did parse.
