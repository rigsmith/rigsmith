---
type: feat
"github.com/rigsmith/rigsmith"
---

The npm wrapper packages can now be published from an already-published release, so one channel failing no longer needs a new version to fix.

`node scripts/npm/build-packages.mjs --from-release v1.5.0 --publish` downloads that release's per-tool archives, verifies each against the release's own `checksums.txt`, unpacks the binaries and publishes the wrappers — the same bytes every other channel already carries, signatures intact. A `npm republish` workflow runs it with the registry token, taking a tag and an optional dry run.

This exists because v1.5.0 published its GitHub release, Homebrew cask, Scoop manifest and five winget PRs, and then skipped npm: a manifest check placed before the npm step failed on a false positive and took the rest of the job with it. Recovering from a laptop was not an option — the wrappers are built from GoReleaser's `dist/`, which holds the *signed* binaries and exists only on the runner that built them, so a local rebuild would have pushed unsigned Windows binaries to npm. The alternative, cutting a fresh version to fix one channel, drags every other channel along with it, including five more winget PRs into a queue that already takes weeks.

Verified end to end against the real v1.5.0 release: 24 binaries unpacked into 29 packages, and the Windows binary inside the built package still carries its Authenticode signature.
