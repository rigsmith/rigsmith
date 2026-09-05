---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

a props file that sets `IsPackable` false for everything and true again under a condition on the project's path is now read rather than tolerated. That rule is how a repo says "what is under src/ packs, what sits beside it does not", and since the condition went unevaluated, every project inheriting the props file came back packable — a workspace holding nullean/mermaider listed its tests, benchmarks, gallery and AOT smoke test alongside the two libraries it ships. A condition testing `MSBuildProjectDirectory` or `MSBuildProjectName` with Contains, StartsWith or EndsWith, optionally negated, is now evaluated against the project's own path. Anything else — a comparison, a compound, a property not known here — is tolerated as a true exactly as before.
