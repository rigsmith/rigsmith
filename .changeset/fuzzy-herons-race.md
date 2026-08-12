---
type: fix
---

rig: discover .NET projects by scanning, not by guessing which solution is "the" solution — a repo with several .slnx files (nested per-package solutions, or a test-only aggregate at the root) no longer hides every project outside the first root-level one from `rig info`/`run`/`build`/`test`. A `solution` pinned in .rig.json still scopes discovery; `rig doctor` now lists .NET projects through that same model, so it can't report a project the verbs can't see.