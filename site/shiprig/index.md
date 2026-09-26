# shipRig

RigSmith's release tool — the Go successor to net-changesets. One uniform
`add changeset → version → publish` workflow across .NET, Node, Go, and Rust,
plus Tauri, Electron, and Velopack desktop apps.

```sh
shiprig init               # release-init wizard: pipeline, forge, publish auth
shiprig add -p my/pkg --bump minor -m "Add a feature"   # interactive without flags
shiprig status --verbose
shiprig version            # bump + changelog, with dependency cascade
shiprig publish            # registries (idempotent, confirm-gated on a TTY)
shiprig release            # the configurable step pipeline
shiprig release --dry-build # build artifacts locally, publish nothing
shiprig release --rehearse  # full local dry run — no commit, tag, or network
shiprig doctor             # health-check changesets + release readiness
shiprig info
```

`version` runs the shared engine in [`rigsmith/core`](/core/): it parses
changesets, cascades bumps to dependents, applies linked/fixed/lockstep
grouping, stamps the new versions into each ecosystem's manifest, and writes
`CHANGELOG.md`.

## The full surface

The whole workflow is wired:

- `init`, `add` (`--since`), `status` (`--since`, `--output`)
- `version` (normal / pre / snapshot, changelog enrichment + `format:`)
- `pre` — enter/exit prerelease mode
- `config` — `show` / `get` / `set` / `path` / `edit` the `.changeset/config.json`
- `info`, `ui`
- `packages` — show every discovered package and what the release does with each
  (its bump, or no change / private / ignored) and include/exclude them via a
  picker that persists the choice to the changeset `ignore` list; `packages list`
  prints and exits, and `packages list --json` prints every package for a script
  (ecosystem, directory, version, next version and bump, private/ignored, and
  where its changelog goes)
- `tag` — create the git tags for the released versions
- `publish-plan` — what a publish would release, as `changeset publish-plan`:
  each package's registry is asked whether its version is already there, and a
  package released by its git tag alone (a Go module, a desktop app, a private
  package with `privatePackages.tag`) is listed `tag-only` when its tag is
  missing, and so is a package already published whose tag never made it. A
  private NuGet feed that asks for credentials gets them, first found wins:
  credentials written into the source URL (`https://user:token@…`), else the
  resolved `dotnet.auth`, else `NUGET_API_KEY` (never in place of a
  `dotnet.auth` that can't be resolved); the account is the URL's user, else
  `dotnet.user`, else a placeholder. They're sent only after it asks, and only
  to the source's own host and port over https (plain http only to a loopback
  host, `localhost`, `127.0.0.0/8` or `::1`, for a local test feed).
  `--output <file>` writes @changesets v3's plan JSON, chunked in dependency
  order, and `--tag` sets the npm dist-tag. A registry that can't be
  reached fails it rather than guessing
- `pack --out-dir <dir>` — build the package file for each release the plan
  would publish (`npm pack`, `dotnet pack`) into `<dir>/packages/`, and write
  `<dir>/publish-plan.json` recording each file and its sha256 integrity, as
  `changeset pack` does; `--from-publish-plan <file>` packs a plan made
  earlier. This is the build half of a split release, where the job holding
  registry credentials never builds. Cargo can't publish a prebuilt crate, so a
  cargo release is refused
- `publish` — idempotent, confirm-gated on a TTY, `--yes` for CI. In pre mode
  npm packages go out under the prerelease tag (`next`, say) rather than
  `latest`, as `changeset publish` does; `--tag <name>` picks another dist-tag
  outside pre mode.
  `--from-pack-dir <dir>` publishes exactly the files `pack` built there, in the
  plan's order and under its npm dist-tag, building nothing, as `changeset
  publish --from-pack-dir` does: each file's sha256 must still match what `pack`
  recorded and its package must be at the plan's version, or nothing is pushed.
  npm and NuGet publish a prebuilt file; cargo can't, so it's refused
- `tag` and `publish` speak @changesets v3's output contract: with
  `--output <file>` or `$CHANGESETS_OUTPUT` set, each appends a
  `{"type":"git-tag","tag":…,"packageName":…}` line per tag it creates, skips a
  tag that already exists locally or on the remote, and pushes nothing. The
  caller owns the push, as with `changeset publish`; this is how changesets/action
  and shiprig-action learn which tags to push and release
- `release` — the [configurable step pipeline](./pipeline) with step filtering
  (`--only` / `--skip` / `--from` / `--to`), `--channels` to build one target
  channel instead of the whole [Velopack](./pipeline#desktop-apps-with-velopack)
  matrix, `--dry-run`, `--dry-build`, `--local`, and `--rehearse`
  (see [local rehearsal](./pipeline#local-rehearsal-dry-run-dry-build-local-rehearse))
- `doctor` — the changeset baseline (git/repo/config/workspace) plus a release
  section: `gh` auth and the publish/build tool each detected ecosystem needs

Beyond the basics, the `release` pipeline adds multi-forge releases
(GitHub / GitLab / Gitea), OIDC trusted publishing + 1Password/secret-manager
auth for npm / crates.io / NuGet, Tengo scripting (`if` gates, computed `vars`,
`script` steps), a cross-platform portable shell, an `issues` step that
comments on and closes resolved issues (GitHub today; GitLab/Gitea are planned),
and code-signing for Tauri / Electron artifacts. See
[the release pipeline](./pipeline).

## shipRig vs changeRig

[`changeRig`](/changerig/) is the changeset lifecycle on its own. shipRig is
everything changeRig does **plus** `tag`, `publish`, `pre`, and `release`. They
share the same `add`/`status`/`version` code, so the changeset half behaves
identically.

- [The release pipeline →](./pipeline)
