---
type: fix
scope: rig
"github.com/rigsmith/rigsmith"
---

`shiprig --version` and `changerig --version` now report the version you installed.

Neither binary was given a version at build time, so both fell through to the description meant for a build from source — and every published copy, from every channel, introduced itself as `source build`, followed by a path on whatever machine it was unpacked onto and the timestamp of the download. There was no other way to ask: `shiprig version` is the command that versions *your* packages, not a report about shiprig. `rig` and `clauderig` were never affected.

Both now carry a version seam filled at release time, in whichever file already calls into the CLI framework — beside `Execute` for shiprig, in `main` for changerig, matching what `rig` and `clauderig` respectively already do. A build from source still says so, which is the point of that description.

A test reads `.goreleaser.yaml` and fails if any released binary lacks its version flag, so a fifth tool cannot join the release and ship unable to name itself.
