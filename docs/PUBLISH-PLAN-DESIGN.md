# Publish plan, pack, and publishing from a pack directory

## Why

@changesets v3 splits a release across jobs so the job holding publish
credentials (npm trusted publishing needs `id-token: write`) never runs the
build. changesets/action's `select-mode`, `pack` and `publish` sub-actions
drive three CLI commands:

| @changesets | Does |
|---|---|
| `changeset publish-plan --output <file>` | writes what would publish: `{version: 1, plan: [[release, …], …]}`, chunks in dependency order; a release is `{kind: "publish", name, version, access, tag}` or `{kind: "tag-only", name, version}` |
| `changeset pack --out-dir <dir> [--from-publish-plan <file>]` | packs each `publish` release into `<dir>/packages/`, and writes `<dir>/publish-plan.json` with each release's `tarball: {path, integrity}` |
| `changeset publish --from-pack-dir <dir>` | publishes those files, building nothing, then tags |

shiprig-action ports the sub-actions once shiprig has the three commands.
shiprig keeps canon's file format, so a workflow moves across unchanged.

## Deciding what publishes

Canon publishes a package whose local version isn't on the registry, and
tags (`tag-only`) a private package whose tag doesn't exist yet, when
`privatePackages.tag` is set. shiprig asks each package's ecosystem through a
new plugin method, `published`:

- **npm**: `npm view <name>@<version> version`, as canon's `npm info`: the
  version printed means published, E404 means not, anything else is an error.
- **NuGet**: the feed's flat container (`PackageBaseAddress/3.0.0` from the
  v3 service index; nuget.org by default). A source given by its NuGet.config
  name can't be resolved and is an error.
- **crates.io**: `GET /api/v1/crates/<name>/<version>`.
- **Go modules, regex packages, Electron, Tauri, Velopack** answer
  `noRegistry`: they release by their git tag (and forge release), so the plan
  lists them `tag-only` when the tag is missing.

A registry that can't be reached is an error, never "not published". A plan
built on a guess would publish or skip the wrong thing, and canon fails the
same way.

## Stages

1. **`published`** in the plugin protocol and every built-in adapter.
2. **`shiprig publish-plan [--output <file>]`**, canon's JSON v1, chunks in
   dependency order (dev dependencies ignored, as canon).
3. **`shiprig pack --out-dir <dir> [--from-publish-plan <file>]`**, through
   the adapters' existing `artifacts` method.
4. **`shiprig publish --from-pack-dir <dir>`**: `PublishRequest` gains the
   artifact path. npm (`npm publish <tgz>`) and NuGet (`nuget push <nupkg>`)
   can publish a prebuilt file. Cargo can't (`cargo publish` builds from
   source), so a cargo release in a pack plan is refused with that reason
   rather than rebuilt behind the caller's back. `tag-only` releases only tag.
5. **shiprig-action's `select-mode`, `pack` and `publish` sub-actions** run
   on shiprig.
