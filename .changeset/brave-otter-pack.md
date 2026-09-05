---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

a library that describes the package it produces is discovered as one, even where nothing declares `IsPackable`, a `PackageId` or a version. A project carrying `PackageTags`, `PackageLicenseExpression`, `PackageProjectUrl`, `PackageIcon`, `PackageReadmeFile` or an item marked `Pack="true"` is saying what goes in its package; plenty of libraries say only that and let CI supply the version on the pack command line, and those were being skipped — in one workspace, five published libraries were invisible beside the demo apps and tests that correctly were. Read from the project itself, never an ancestor props file — repo-wide licence and author metadata says nothing about which projects beneath it pack. `IsPackable` false still wins, and assembly metadata that a non-packing project carries quite legitimately — `Title`, `Authors`, `Description`, `RepositoryUrl` — is not read as packaging intent.
