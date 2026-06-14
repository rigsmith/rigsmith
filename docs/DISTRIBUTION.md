# Distribution

rigsmith ships two binaries today (`rig`, `shiprig`) from a single go.work
monorepo, with `changerig` planned. All channels are driven by the
[GoReleaser config](../.goreleaser.yaml) and the tagged GitHub Release it
produces.

## Channels

| Channel | Status | Notes |
| --- | --- | --- |
| **GitHub Releases** | ✅ primary | GoReleaser builds linux/darwin/windows on amd64+arm64, uploads `.tar.gz` (unix) / `.zip` (windows) archives + `checksums.txt`. |
| **`curl \| sh`** | ✅ | `curl -fsSL https://rigsmith.sh \| sh` (all tools) or `… \| sh -s shiprig` for one. Served by [`scripts/install.sh`](../scripts/install.sh); installs to `~/.local/bin` (override with `RIGSMITH_INSTALL`). |
| **Homebrew tap** | ⛔ TODO | Skeleton `brews:` block is in `.goreleaser.yaml`, commented out until a `rigsmith/homebrew-tap` repo exists. Target: `brew install rigsmith/tap/rig`. |
| **Scoop** | ⛔ TODO | Windows. Add a `scoops:` block + a `rigsmith/scoop-bucket` repo. Target: `scoop install rig`. |
| **npm binary wrapper** | ⛔ TODO (secondary) | Thin npm package(s) (`@rigsmith/rig`, `@rigsmith/shiprig`) whose `postinstall`/`bin` shim downloads the matching GitHub Release archive — lets Node users `npx rig` / add it as a devDependency. Secondary to the native channels above. |

## Cutting a release locally

Prereqs: [`goreleaser`](https://goreleaser.com) installed, a clean tree, and a
`GITHUB_TOKEN` with `repo` scope exported.

```sh
# 1. Sanity-check the config (run after any .goreleaser.yaml edit).
goreleaser check

# 2. Dry run — builds everything into ./dist without publishing.
goreleaser release --snapshot --clean

# 3. Tag the release. GoReleaser derives the version from the tag.
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0

# 4. Publish: build all targets, create the GitHub Release, upload archives
#    + checksums, and (once enabled) update the Homebrew/Scoop buckets.
goreleaser release --clean
```

To preview just the archives without tagging, `goreleaser build --snapshot --clean`
produces the binaries under `./dist`.

## Notes

- Neither main package currently exposes a `version` variable, so builds only
  strip symbols (`-s -w`). Add `-X main.version={{.Version}}` to the relevant
  build's `ldflags` once a `var version string` lands in that `main`.
- Repo slug placeholders (`rigsmith/rigsmith`) in `.goreleaser.yaml` and
  `scripts/install.sh` must be updated to the real public repo before the first
  release.
