---
type: fix
scope: changerig
"github.com/rigsmith/rigsmith"
---

A version chosen with `--release-as` (or at the version prompt) labels the release by the move it actually makes: major if the major version changes, minor if the minor does, else patch. `--release-as lib=2.0.0` on a patch changeset used to print `patch  lib  1.2.0 → 2.0.0` and head the 2.0.0 entry `### Patch Changes`; it now says `major` and `### Major Changes`, for each package it names, fixed or linked group members included, and with `bumpMinorPreMajor` a forced 1.0.0 is a major too. The changes that decided the release move under that heading and smaller ones keep their own. Without `--release-as` nothing is relabelled: a linked or fixed group's coordinated version keeps each member's changes under their own bump, as @changesets does, and prerelease and snapshot labels are unchanged. A 0.x release that `bumpMinorPreMajor` makes a minor lists its major change under `### Minor Changes`, as `status` reports it. Typed sections are unchanged, so a breaking change keeps 💥 Breaking Changes.
