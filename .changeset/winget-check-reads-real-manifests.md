---
type: fix
"github.com/rigsmith/rigsmith"
---

The winget manifest check now reads the manifests GoReleaser actually writes, and runs last so it can't block a publish.

Its first live run failed all five correct manifests. The check was written against the *published* manifest shape, where winget's publish pipeline has flattened the keys to column 0; in what GoReleaser generates they sit inside each entry of the `Installers:` list, indented. The `^NestedInstallerType: portable$` anchor therefore matched nothing, anywhere, ever — the check could only pass a manifest that would never exist.

It cost more than a red tick. The step sat before the npm publish, so v1.5.0 published its GitHub release, Homebrew cask and five winget PRs, and then skipped npm entirely, leaving that channel a version behind a release that was otherwise fine.

Two changes:

- **It reads the real shape**, matching the keys wherever they are indented, and counts per installer rather than per file: a manifest with x64 portable and arm64 not is broken for half its users and used to pass. Fixtures are the bytes GoReleaser produced for the RigSmith.Rig 1.5.0 submission, captured verbatim, with the half-broken variant alongside — `go test ./scripts/` runs the script against both, so the check that gates a release is itself covered.
- **It runs last.** The winget PRs are already open by the time it can look at them, so it is an alarm, not a gate: it has nothing left to prevent and everything left to break. A reporting check must not sit upstream of a publish.
