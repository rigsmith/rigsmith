---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

A version override labels the release by the move it actually makes: major if the major version changes, minor if the minor does, else patch. `--release-as lib=2.0.0` on a patch changeset used to print `patch  lib  1.2.0 → 2.0.0` and head the 2.0.0 entry `### Patch Changes`; it now says `major` and `### Major Changes`, for a single package or a fixed or linked group alike, and with `bumpMinorPreMajor` a forced 1.0.0 is a major too. An untyped entry lists the changes that decided the release under that bump and keeps smaller ones under their own headings; a 0.x release that `bumpMinorPreMajor` makes a minor lists its major change under `### Minor Changes`, as `status` reports it. Typed sections are unchanged, so a breaking change keeps 💥 Breaking Changes. Prerelease and snapshot versions are judged on their stable part, so their labels don't change.
