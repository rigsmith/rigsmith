---
type: docs
"github.com/rigsmith/rigsmith"
---

the stack workspace guide now covers adopting a project you already have. The page previously assumed every repo — including the app consuming the forks — lived inside the workspace, which is the rarer layout: your own project normally stays outside it and reaches in. A new section lays out both topologies and, more importantly, how many build overlays each one needs, since the consumer-outside layout needs two and the second is easy to miss. The wiring section now shows both, including the `Exists()` gate that makes the consumer's overlay safe to commit — machines without the workspace build from packages exactly as before. Added along with them: how to verify a swap by asking MSBuild what it evaluated rather than trusting a green build, the four MSBuild traps that cost the most time in practice, and two behaviours that bite quietly — private upstreams are not supported by the import yet, and a build without the workspace silently falls back to the published package rather than failing.
