---
"github.com/rigsmith/rigsmith": minor
---

shiprig release: three ergonomics features that let custom pipelines (e.g. a local Velopack desktop release) stay declarative instead of falling back to hand-written shell.

- **`${version}` is now the new (bumped) version**, resolved from the pending changesets at plan time — so it is correct in `--dry-run` and in every step, with no need to re-read the bumped value out of a manifest. Adds `${lastVersion}` (the pre-bump version) and `${nextVersion}` (an explicit alias of `${version}`), each with addressed (`${lastVersion.<pkg>}`) and aggregate (`${lastVersions}`/`${nextVersions}`) forms; also exposed on the script `ctx`.
- **`commit.paths`** scopes the release commit to the listed paths (`git add -- <paths>`) instead of `git add -A`, keeping unrelated working-tree changes out of the release commit.
- **`tagTemplate`** (changeset config) overrides the git tag, e.g. `"v${version}"` for the single-app `vX.Y.Z` convention. Honored consistently by the tag, publish, and forge-release steps and the `${tag}` variable. Placeholders: `${version}`, `${name}`.

See `examples/velopack-desktop/` for a worked configuration.
