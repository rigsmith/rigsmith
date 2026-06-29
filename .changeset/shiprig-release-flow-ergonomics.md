---
"github.com/rigsmith/rigsmith": minor
---

shiprig release: three ergonomics features that let custom pipelines (e.g. a local Velopack desktop release) stay declarative instead of falling back to hand-written shell.

- **`${version}` is now the new (bumped) version**, resolved from the pending changesets at plan time — so it is correct in `--dry-run` and in every step, with no need to re-read the bumped value out of a manifest. Adds `${lastVersion}` (the pre-bump version) and `${nextVersion}` (an explicit alias of `${version}`), each with addressed (`${lastVersion.<pkg>}`) and aggregate (`${lastVersions}`/`${nextVersions}`) forms; also exposed on the script `ctx`.
- **`commit.paths`** scopes the release commit to the listed paths (`git add -- <paths>`) instead of `git add -A`, keeping unrelated working-tree changes out of the release commit.
- **Single-app repos default to the `vX.Y.Z` git tag.** A repo with exactly one discovered, non-Go package has no sibling name to disambiguate, so the tag now defaults to `vX.Y.Z` instead of `<name>@<version>`. (A repo with a second package — even an ignored one — stays on `<name>@<version>`.) **BREAKING** (treated as a minor for now): a single-package non-Go repo that was tagging `name@version` will switch to `vX.Y.Z` on its next release — set `tagTemplate: "${name}@${version}"` to keep the old tags. Go is unaffected (its `dir/vX.Y.Z` module-path tags are required for `go get`, and a root module already tags `vX.Y.Z`).
- **`tagTemplate`** (changeset config) overrides the git tag for any repo, e.g. `"v${version}"` or `"${name}@${version}"`. Honored consistently by the tag, publish, and forge-release steps and the `${tag}` variable. Placeholders: `${version}`, `${name}`.

See `examples/velopack-desktop/` for a worked configuration.
