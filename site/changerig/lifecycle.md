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
changerig add -p my/pkg -t fix -m "Stop the crash"     # type-driven bump (! = breaking)
changerig add -p my/pkg -t feat --scope rig -m "…"     # files the entry under rig:
changerig add                 # interactive: pick packages, bump, message, scope
```

`--package` can be omitted only where there is one package to choose; with
several, `add` says so rather than writing a changeset that names none — such a
file is silently ignored by every later step.

Writes a `.changeset/*.md` file in the shared @changesets format: which
packages change, at what bump level (`major`/`minor`/`patch`), and a summary
line that becomes the changelog entry.

### Type and scope

A changeset can carry a conventional **type** and **scope**, either as
frontmatter or as a `feat(rig): …` prefix on the summary:

```md
---
type: feat
scope: rig
"github.com/you/repo"
---

new `rig stack` — several repos fused into one history
```

They do different jobs. The **type** picks the changelog section and, when no
explicit bump is given, decides the bump (`feat` → minor, `fix` → patch, per
[`changelogGroups`](./index)). The **scope** names which tool the entry belongs
to: it becomes the bullet's lead-in and groups that tool's lines together
within a section. Neither is required — an untyped changeset still
renders under the section for its bump (`Minor Changes`, `Patch Changes`) as
before.

Note the package line above carries no bump. Leave it off and the type decides;
give one and it wins, per package — which is how one changeset can be a feature
for an app and a patch for the library under it.

`--scope` is inferred from what the branch changed, so it is one less thing to
remember: a diff confined to `cmd/rig` or `internal/rig` infers `rig`, and a
diff spanning several tools infers nothing rather than guessing.

A `!` on the type marks a breaking change — `feat(rig)!: …`, or `type: feat!` —
which forces a major bump and renders under **💥 Breaking Changes**, ahead of
every other section.

### Choosing the sections and their order

`changelogGroups` maps each type to a heading and an implied bump, and the list
order is the section order. Drop the emoji, rename a section, or move fixes
above features by rewriting it:

```jsonc
{
  "changelogGroups": [
    { "type": "feat", "section": "Features", "bump": "minor" },
    { "type": "fix",  "section": "Fixes",    "bump": "patch" }
  ]
}
```

`changelogScopes` does the same for scopes *within* a section — which tool a
reader sees first:

```jsonc
{ "changelogScopes": ["rig", "clauderig"] }
```

Scopes left out follow alphabetically, and unscoped entries come last. Omit the
key entirely and every scope sorts alphabetically.

Flags:

| Flag | Meaning |
|------|---------|
| `-p, --package` | Package to include (repeatable) |
| `--bump` | Explicit bump: `major` / `minor` / `patch` / `auto` |
| `-t, --type` | Conventional type (`feat`/`fix`/…, suffix `!` for breaking); the bump derives from it when `--bump` is omitted |
| `-m, --message` | Changeset summary (skip the prompt) |
| `--empty` | Write an empty changeset that names no packages |
| `--scope` | Which part of the repo the change belongs to — the tool, in a monorepo. Inferred from the changed files when omitted; `-` for none |
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
(`{tag}`/`{commit}`/`{datetime}`/`{timestamp}` suffix) for snapshot releases,
`--independent` to version each package separately instead of via a shared
version file, and `-y, --yes` to accept the computed versions without the
interactive override prompt.

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
