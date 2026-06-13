# rigsmith release action

A composite GitHub Action that runs the release loop with `relrig`, mirroring
[`@changesets/action`](https://github.com/changesets/action) (and its .NET
predecessor in net-changesets):

- **When changesets are pending** it runs the `version` command, commits the result to a
  `changeset-release/<branch>` branch, and opens (or updates) a **Version Packages** pull request.
- **When no changesets are pending** (that PR was merged) it runs the `publish` command, which
  pushes packages to their registries and creates git tags.

It is **polyglot for free**: `relrig` runs the same engine across .NET, Node, Go, and Rust, so one
`version`/`publish` drives every ecosystem in the repo.

Unlike the net-changesets action, this one **installs `relrig` for you** (`install: true`, the
default) — no separate setup step. Set `install: false` if you already put `relrig` on `PATH`.

## Inputs

| Input          | Default              | Description                                                              |
| -------------- | -------------------- | ----------------------------------------------------------------------- |
| `version`      | `relrig version`     | Command run to update versions and changelogs when changesets exist.    |
| `publish`      | `''`                 | Command run to publish when there are none. Empty skips publishing.     |
| `status`       | `relrig status`      | Command run to render the pending plan into the PR body.                |
| `cwd`          | `.`                  | Working directory.                                                      |
| `branch`       | `changeset-release/<branch>` | Release branch the version PR is opened from.                   |
| `title`        | `Version Packages`   | Title and commit message for the version PR.                            |
| `setupGitUser` | `true`               | Set the git user to `github-actions[bot]`.                              |
| `install`      | `true`               | Auto-install `relrig` before running. `false` ⇒ assume it's on `PATH`.  |
| `relrigVersion`| `latest`             | `relrig` version to install (`latest`, `v1.2.3`, `main`, …).            |

## Outputs

| Output              | Description                                                       |
| ------------------- | ---------------------------------------------------------------- |
| `hasChangesets`     | `true` when a version PR was opened or updated.                  |
| `published`         | `true` when at least one package was published.                  |
| `publishedPackages` | JSON array of `{ name, version }` that were published.           |
| `pullRequestNumber` | The number of the opened or updated version PR.                  |

## Usage

```yaml
name: Release
on:
  push:
    branches: [main]

permissions:
  contents: write       # push the release branch + tags
  pull-requests: write  # open the Version Packages PR

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0   # tags + full history for status --since / publish

      - name: Release
        uses: rigsmith/rigsmith/.github/actions/release@main
        with:
          version: relrig version
          publish: relrig publish --yes
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          # plus any registry credentials your publish needs (NUGET_API_KEY, NPM_TOKEN, …)
```

`GITHUB_TOKEN` is read by the `gh` CLI for PR creation and must be present in the job environment.
The auto-installer also passes it to lift the GitHub API rate limit when resolving `latest`.

## How install works

`install-relrig.sh` first downloads the goreleaser release asset
(`relrig_<version>_<os>_<arch>.tar.gz`) for the runner. If no matching asset exists for the
requested version (e.g. during the pre-release period, or `relrigVersion: main`), it falls back to
`go install github.com/rigsmith/release@<version>` — so a `setup-go` step makes the fallback
available. Once `rigsmith/rigsmith` cuts tagged releases, the download path needs no toolchain.

## Notes / limitations (v1)

- The PR body is the plain `status` output; it does not yet render per-package release notes.
- `publishedPackages` is parsed from `relrig publish`'s `published name@version` and
  `tagged+pushed module/vX.Y.Z` lines (best-effort; `NO_COLOR` is set so the output is plain).
- It does not create GitHub Releases yet.
- For the per-PR "add a changeset" gate, see the sibling
  [`require-changeset`](../require-changeset/) action.
