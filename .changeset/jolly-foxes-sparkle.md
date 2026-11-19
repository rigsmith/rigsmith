---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`tag --json` prints every tag a run makes (or, with `--dry-run`, would make) and every one already present, each with the packages it covers by name, version and ecosystem. A script can read which packages a tag releases from it instead of guessing from the tag's name, so a lockstep repo sharing `"tagTemplate": "v${version}"`, or any custom `tagTemplate`, reads correctly. Tag events (`--output` or `$CHANGESETS_OUTPUT`) from `tag` and `publish` now give a tag several packages share one `git-tag` event per package. Before, only its first package was reported, and the others looked unreleased.
