---
type: feat
scope: rig
"github.com/rigsmith/rigsmith/core"
---

Ecosystem adapters can now be asked to make a package resolve from sources in the tree instead of from a registry, and to report what would stop that happening. `rig stack doctor` uses it to check a stackspace's build wiring, and says so when it finds a member whose own build file quietly ends MSBuild's search — the failure that leaves every project beneath it resolving published packages while the build succeeds and says nothing.

The capability is not specific to a stackspace. Any repository holding both a package and something that consumes it through a registry can ask for the same redirect, so it sits on the ecosystem contract beside discovery and publishing rather than inside `rig stack`. Adapters advertise it like any other method; those with no such mechanism report that they skipped rather than failing.

Two things the ecosystem kit could not previously answer are now part of it. Discovery reports a dependency on a package the repo also produces but reaches through the registry, marked as such — invisible before, since only project references counted, and it is exactly what an overlay redirects. And a caller that cares about a package's identity rather than its release readiness can ask for projects carrying no version in the tree, such as a library whose version is stamped by its own CI. The release cascade ignores both additions deliberately, so nothing about versioning or publishing changes.
