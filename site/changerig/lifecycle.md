# The lifecycle

changeRig follows the @changesets model: contributors describe *intent* in small
markdown files, and a later `version` step turns the accumulated intent into
version bumps and a changelog.

## `init`

```sh
changerig init
```

Creates the `.changeset/` directory with a `config.json`. The config schema
(`.changeset/config.json`) covers changelog/format specs and ignore globs.

### Where the config lives

`init` writes the canonical `.changeset/config.json`, but the config is
**resolved** from one of these locations (at most one — more than one is an
error that lists them; a `.json` + `.jsonc` pair counts as two):

- `.changeset/config.jsonc` · `.changeset/config.json`
- `.changeset/changerig.jsonc` · `.changeset/changerig.json`
- `changerig.jsonc` · `changerig.json` (repo root)
- a `"changerig"` (or `"changeset"`) key inside `.rig.json`

`.changeset/config.json` keeps the @changesets layout so the JS tool reads it
too; the alternate names and the `.rig.json` key are rigsmith conveniences.
`changerig config set` edits whichever single file is in use (when the config
lives in a `.rig.json` key, edit it there).

## `add`

```sh
changerig add -p my/pkg --bump minor -m "Add a feature"
changerig add                 # interactive: pick packages, bump, message
```

Writes a `.changeset/*.md` file in the shared @changesets format: which
packages change, at what bump level (`major`/`minor`/`patch`), and a summary
line that becomes the changelog entry.

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
```

Consumes the pending changesets and:

1. parses them via the core engine,
2. cascades bumps to dependents (range-aware),
3. applies **linked / fixed / lockstep** grouping,
4. stamps the new version into each ecosystem's manifest, and
5. writes `CHANGELOG.md`.

Changelog generators are **pluggable** — the built-in renderer dogfoods the same
JSON contract external plugins speak. Set `"changelog": "<plugin>"` in config to
swap it in.
