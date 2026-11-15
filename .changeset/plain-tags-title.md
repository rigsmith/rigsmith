---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

shiprig titles each per-package forge release after its tag again, as changesets' own action does: a single-package repo's release on `v1.2.3` is titled `v1.2.3` rather than `<package>@1.2.3`, a monorepo's `@scope/pkg@1.2.3` keeps that title, and a `tagTemplate` or Go `dir/v1.2.3` tag is the title as it renders. This restores the tag-named titles single-package repos had before shiprig-action moved onto `shiprig forge-release`. It applies to the `release` step and `shiprig forge-release` on GitHub, GitLab and Gitea, and `forge-release --json` reports that title as `name`. Combined releases are unchanged and still take their title from `forgeRelease.name`.
