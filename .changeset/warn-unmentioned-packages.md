---
"github.com/rigsmith/rigsmith": minor
---

changerig: `status` and `version` now warn when a changeset names several packages but its body only talks about some of them — because one body is rendered verbatim into every changelog it names, so the packages you were not thinking about get an entry about something else. The warning names the packages that will get the text and suggests splitting; it never blocks a release, and stays quiet for packages in the same `fixed` or `linked` group, which share a body by design.
