---
type: docs
"github.com/rigsmith/rigsmith"
---

The stack workspace guide now draws the three things that were hardest to picture from prose alone. A diagram of the round trip shows each upstream repo's history arriving under its own directory, one commit spanning several of those directories, and each project leaving again as a branch on your fork with a pull request against its own upstream — which makes it plain at a glance that importing and sending are not mirror images, since only the inbound half goes through josh. A second diagram counts build overlays by counting directory trees, the quickest way to see why keeping your own project outside the workspace needs two of them and why the workspace's own overlay is the one people forget. A third shows the single project file that never gets edited resolving two different ways depending on whether the workspace is sitting next door, so the fallback to the published package is visible rather than implied. The documentation site renders Mermaid diagrams now, and they follow the light and dark toggle.
