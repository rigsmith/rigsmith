---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

Two packages with the same name are now an error, naming both, instead of one silently standing in for the other. Changesets name packages by name, so an npm package and a Go module both called `shared` used to share one bump and changelog, and a second npm package with a name already taken was dropped from discovery without a word. Rename one, or narrow discovery so only one is found (`paths`, an ecosystem's `sourcePath`, or a regex ecosystem's `packages` list); `ignore` can't separate them, since it matches by name too.