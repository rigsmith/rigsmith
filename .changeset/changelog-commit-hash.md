---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

Changelog commit references tell apart two commits that share their first 7 characters. `@changesets/changelog-git` and `@changesets/changelog-github` lines show git's unique abbreviation, which is the same 7 characters as @changesets except where 7 is ambiguous, so two such commits no longer render the same hash. A `changelog-github` commit link now points at the full SHA, as @changesets' does, and an external changelog generator receives each change's `commit` as the full SHA, as @changesets hands `getReleaseLine`, instead of 7 characters.
