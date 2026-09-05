---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

a .NET project whose version is computed at build time — MinVer from git tags, a CI-stamped build — is discovered as a package. It declares no `<Version>`, and discovery used to take that as "not a package"; a project that is `IsPackable`, declares a `PackageId`, or references MinVer now comes back with no version in the tree, which `info` and `status` say, and the version a release computes for it is recorded in `.changeset/versions.json` and bumped from next time.
