---
type: fix
"github.com/rigsmith/rigsmith"
---

`status` and `doctor` now name the changesets that can never release, instead of reporting "nothing to release" and leaving the contradiction to a human.

A changeset whose front matter omits the package line parses cleanly, counts as pending, and is attributed to no package — so it contributes to no bump and no changelog, forever. Nothing surfaced that: `status` printed "Changesets found, but nothing to release" while holding sixteen of them, and `doctor` cheerfully reported "16 changeset(s)" pending. In this repo they accumulated across a full release cycle; 1.5.0 nearly shipped three weeks of merged work with no changelog entry, and the oldest stranded file had been sitting there since before 1.4.0.

The tool already knew. It has the changesets and the package list, so it can say which file is inert and why — `status` now lists each one with its cause (names no package / names unknown package(s) / names only ignored package(s)) and, when exactly one package is releasable, prints the exact command that writes it correctly:

```
Changesets found, but nothing to release.

  brave-lamps-hum      names no package
  lonely-otters-drift  names no package
  typo-comets-race     names unknown package(s): example.com/nope

Name the package in the front matter — `changerig add -t <type> -p example.com/demo` writes it correctly.
```

`doctor` gains a `changeset targets` check carrying the same finding as a warning, so it surfaces without having to run `status` and read past a line that says everything is fine.

A changeset naming at least one releasable package is never reported, including one that also names ignored packages — the empty plan is not that file's doing.
