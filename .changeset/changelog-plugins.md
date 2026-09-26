---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

Custom changelog plugins get more to work with:
- the options you give them in the config, as in `["./scripts/changelog.js", { "style": "terse" }]`;
- each change's full commit hash, plus its pull request and author when you set a `repo` option;
- the released dependencies as a list;
- whether the version was picked with `--release-as`.

A plugin's output now always ends with exactly one newline.
