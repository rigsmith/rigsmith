---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

In a shallow clone (CI's default checkout), changelog entries from `@changesets/changelog-github` or `@changesets/changelog-git` no longer all link the release commit. As `@changesets/git` does, the commit that added each changeset is looked up by deepening the clone until it's found, instead of taking the clone's oldest commit, where every file looks added.
