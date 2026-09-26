---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

`doctor` reports every problem it finds. A manifest edited by hand no longer hides recorded releases that were never tagged: each gets its own `release record` row. A changeset that can't be parsed, which stops `status` and `version`, is now a failing `changeset files` check listing every such file with its parse error, one per line, where doctor used to skip the pending changesets and report all good. Doctor reads the files `status` and `version` read: `.changeset/pre/` too on the run after `pre exit`, and no changeset files at all when `versioning.source` is `commits`. The other checks still run on the changesets that did parse.
