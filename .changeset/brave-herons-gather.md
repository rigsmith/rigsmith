---
type: feat
scope: shiprig
"github.com/rigsmith/rigsmith"
---

shiprig can make one forge release per version instead of one per package. Set `"forgeRelease": { "mode": "combined", "name": "Acme ${version}", "tag": "v${version}" }` in `.changeset/config.json` and the `release` step makes a single release for everything released at the release commit: tagged from the `tag` template (default `v${version}`, created at the release commit and pushed with the per-package tags, which are unchanged), titled from the `name` template, marked latest on GitHub, a prerelease when any released version is one, with each package's changelog entry under its own heading in package-name order, and every package's assets attached. If a template uses `${version}` and the released packages don't share one, `shiprig release` and `shiprig publish` stop before publishing, naming each package and version (a changesets `fixed` group keeps them on one). The default stays one release per package. New `shiprig forge-release` makes the forge release(s) for the packages tagged at HEAD under the configured mode, for CI that runs `shiprig publish` itself: idempotent, with `--dry-run` and `--json` output naming each release created or skipped and its URL.
