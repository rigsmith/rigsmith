---
type: feat
scope: changerig
"github.com/rigsmith/rigsmith"
---

An external changelog generator gets what the built-in renders from. Each change carries its `commit`, and its `pr` and `author` too when the generator's options name a `repo`. The released dependencies arrive as `dependencyUpdates` (`{name, displayName, newVersion}`) instead of an "Updated dependencies" change it would have to re-parse. A generator's output now ends in exactly one newline, so the next entry no longer runs onto its last line.
