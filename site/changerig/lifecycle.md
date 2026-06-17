# The lifecycle

changeRig follows the @changesets model: contributors describe *intent* in small
markdown files, and a later `version` step turns the accumulated intent into
version bumps and a changelog.

## `init`

```sh
changerig init
changerig init --source commits    # changesets | commits | both
```

Creates the `.changeset/` directory with a `config.json`. The config schema
(`.changeset/config.json`) covers changelog/format specs and ignore globs.
`--source` picks where releases are sourced from — accumulated changeset files,
conventional-commit messages, or both (interactive when the flag is omitted).

### Where the config lives

`init` writes the canonical `.changeset/config.json`, but the config is
**resolved** from one of these locations (at most one — more than one is an
error that lists them; a `.json` + `.jsonc` pair counts as two):

- `.changeset/config.jsonc` · `.changeset/config.json`
- `.changeset/changerig.jsonc` · `.changeset/changerig.json`
- `changerig.jsonc` · `changerig.json` (repo root)
- a `"changerig"` (or `"changeset"`) key inside `.rig.json`
- a `"changeset"` key inside a `shiprig.jsonc` / `release.jsonc` — so a single
  shiprig config file can carry the changeset config too (see
  [the release pipeline](/shiprig/pipeline#one-file-for-both-tools))

`.changeset/config.json` keeps the @changesets layout so the JS tool reads it
too; the alternate names and the `.rig.json` key are rigsmith conveniences.
`changerig config set` edits whichever single file is in use (when the config
lives in a `.rig.json` key, edit it there).

## `add`

```sh
changerig add -p my/pkg --bump minor -m "Add a feature"
changerig add -t fix -m "Stop the crash"   # type-driven bump (suffix ! = breaking)
changerig add                 # interactive: pick packages, bump, message
```

Writes a `.changeset/*.md` file in the shared @changesets format: which
packages change, at what bump level (`major`/`minor`/`patch`), and a summary
line that becomes the changelog entry.

Flags:

| Flag | Meaning |
|------|---------|
| `-p, --package` | Package to include (repeatable) |
| `--bump` | Explicit bump: `major` / `minor` / `patch` / `auto` |
| `-t, --type` | Conventional type (`feat`/`fix`/…, suffix `!` for breaking); the bump derives from it when `--bump` is omitted |
| `-m, --message` | Changeset summary (skip the prompt) |
| `--empty` | Write an empty changeset that names no packages |
| `--since <ref>` | Preselect packages changed since a git ref in the picker |
| `--open` | Open the created changeset in `$EDITOR` |

## `status`

```sh
changerig status --verbose
```

Shows the pending release plan — every package that will bump, the level, and
why (including the dependency **cascade**: a dependent is patch-bumped when one
of its dependencies releases). Supports `--since` and `--output`.

## `version`

```sh
changerig version
changerig version --dry-run        # print the plan without writing files
changerig version --snapshot        # snapshot release (optional tag; bare --snapshot works)
changerig version --independent     # version each package on its own changesets
```

Consumes the pending changesets and:

1. parses them via the core engine,
2. cascades bumps to dependents (range-aware),
3. applies **linked / fixed / lockstep** grouping,
4. stamps the new version into each ecosystem's manifest, and
5. writes `CHANGELOG.md`.

Flags: `-n, --dry-run` (plan only), `--snapshot [tag]` and `--snapshot-template`
(`{tag}`/`{commit}`/`{datetime}`/`{timestamp}` suffix) for snapshot releases, and
`--independent` to version each package separately instead of via a shared
version file.

Changelog generators are **pluggable** — the built-in renderer dogfoods the same
JSON contract external plugins speak. Set `"changelog": "<plugin>"` in config to
swap it in.

## `pre`

```sh
changerig pre enter next     # enter prerelease mode tagged "next" (1.2.0-next.0)
changerig pre exit           # leave prerelease mode; the next version is a normal release
```

Prerelease mode makes `version` produce tagged pre-releases (e.g. `-next.N`)
until you exit. The mode is tracked in `.changeset/pre.json`.

## `changelog`

Hand-author changelog entries outside the changeset flow — useful for notes that
don't map to a version bump:

```sh
changerig changelog add -m "Document the new flag" -t docs   # prepend an entry
changerig changelog add -m "…" --version 1.4.0               # file under a release heading
changerig changelog format my/pkg                            # reformat a CHANGELOG.md
```

`add` prepends an entry under an `Unreleased` heading by default (`--version`
files it under a specific release; `-t/--type` adds a label). `format`
re-runs the native markdown formatter over a package's `CHANGELOG.md`.

## `browse`, `info`, `config`

- `changerig browse` (alias `ls` / `list`) — browse and manage the pending
  changesets.
- `changerig info` — show the resolved config and the packages discovered across
  every ecosystem.
- `changerig config` — `show` / `get` / `set` / `path` / `edit` the
  `.changeset/config.json` (comment-preserving writes).
