# The lifecycle

changeRig follows the @changesets model: contributors describe *intent* in small
markdown files, and a later `version` step turns the accumulated intent into
version bumps and a changelog.

## `init`

```sh
changerig init
changerig init --source commits    # changesets | commits | both
changerig init --changelog github  # link commits and pull requests (a GitHub repo)
```

Creates the `.changeset/` directory with a `config.json`. The config schema
(`.changeset/config.json`) covers changelog/format specs and ignore globs.
`--source` picks where releases are sourced from — accumulated changeset files,
conventional-commit messages, or both (interactive when the flag is omitted).
On a GitHub repository, init also offers
[commit and pull request links](#links) in the changelog: `--changelog github`
turns them on, `--changelog default` keeps @changesets' plain layout, and
without the flag it asks at a terminal. Run from a script, it keeps the plain
layout and prints how to switch.

With commits as a source, a conventional commit whose type is a changelog
group's (or `ci`, `style`, `revert`) releases the packages whose files it
touches; a merge or a freeform message releases nothing. Neither does
housekeeping, as @unjs/changelogen skips it: a non-breaking `chore(deps)` (a
dependency bot's bump) or `chore(release)`, and a release commit itself, which
touches every package it versioned: `chore: release`, `chore: release 1.2.0`,
`chore: release core@1.2.0, ui@0.5.0`, `chore: release 5 packages`, or
release-please's `chore(main): release 1.2.0`. The description must be exactly
one of those, so `chore: release notes 1.2.0` still counts (and so does
release-please's per-component `release core 1.2.0`, whose component can't be
told from a word), and so does a breaking one, by its `!` or a
`BREAKING CHANGE:` footer.

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
within a section. Neither is required — an entry with no typed changes still
renders under the sections for its bumps (`Minor Changes`, `Patch Changes`),
exactly as @changesets does. An entry that does have typed changes doesn't mix
the two styles: its untyped changes join the typed section their bump stands
for (a major the 💥 Breaking section, a minor the `feat` group's, a patch the
`fix` group's), keeping the bump heading only when `changelogGroups` names no
such section, and its released dependencies get a 🌊 Dependencies section of
their own, one bullet each, after the groups.

Note the package line above carries no bump. Leave it off and the type decides;
give one and it wins, per package — which is how one changeset can be a feature
for an app and a patch for the library under it.

`--scope` is inferred from what the branch changed, so it is one less thing to
remember: a diff confined to `cmd/rig` or `internal/rig` infers `rig`, and a
diff spanning several tools infers nothing rather than guessing.

A `!` on the type marks a breaking change — `feat(rig)!: …`, or `type: feat!` —
which renders under **💥 Breaking Changes**, ahead of every other section, and
derives a major bump. Like any derived bump, an explicit per-package bump on the
changeset still wins, so `"pkg": patch` with `feat!` releases a patch.

### Before 1.0.0 {#bump-minor-pre-major}

As @changesets does, a major bump on a `0.x` package releases `1.0.0`. With
`"versioning": { "bumpMinorPreMajor": true }` (release-please's
`bump-minor-pre-major`), a major below `1.0.0` releases as a **minor** instead:
`0.3.0` → `0.4.0`, and `status` reports it as the minor it is. That applies
whether the major comes from a changeset's bump or a breaking type (`feat!`),
and packages at `1.0.0` or above are unaffected. `^0.3.0` doesn't cover
`0.4.0`, so dependents still follow it. Going to `1.0.0` is then a deliberate
step: `version --release-as <pkg>=1.0.0` (or `releaseAs` in shiprig-action's
config).

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

With `--output`, each release also carries a `group`: the packages that have
to be versioned together, named by the group's first member. Packages share a
group when one changeset names both, when one depends on the other in the
plan, when they're in the same fixed or linked group, or when they share a
version file. `version --only` takes a group at a time, which is how a release
can go out in parts (a version PR per group, say).

`--since <ref>` narrows the plan to what the branch adds since that ref, the
way a pull request's status check wants it: the changesets it adds or edits
and, when commits are a versioning source, the commits it adds (those between
the merge-base of the ref and `HEAD`). Changesets and commits already on the
base branch stay out, and so does a prerelease's graduation (the run after
`pre exit`) unless the branch itself changed `.changeset/pre.json`. With
`--output`, a branch with nothing to release still gets the empty plan, in
every source mode.

It doubles as the CI gate, as `changeset status` does: it fails when a package
that would version (not ignored, and not private unless `privatePackages.version`
is set) changed since `--since`, or the base branch by default, and there is no
changeset at all. Nothing pending with nothing changed is not a failure: it
exits 0, and `--output` writes an empty plan (`{"releases": []}`), which is how a
script tells "nothing to release" from an error.

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

With no pending changesets it exits 1 ("no unreleased changesets found"), as
`changeset version` does; a release job should check for changesets first, the
way the release action does.

Private packages (`"private": true`) are treated as ignored unless the config
sets `"privatePackages": { "version": true }`: never versioned, a changeset
naming one is left in place, and one mixing it with a public package is an
error. This is @changesets v3's default.

`--only <package>` (repeatable) is the other way round, and shiprig's own:
version only the named packages, leaving every other one's changesets for a
later run. Unlike `--ignore`, it combines with `ignore` in the config. The
named packages must be whole groups (`status --output` lists each package's
group); naming part of one is refused, since a changeset would be split, a
dependent left behind its dependency, or a fixed or linked group or shared
version file released in pieces. The refusal names what's missing.

`--ignore <package>` (repeatable) leaves packages out of one run, as
`changeset version --ignore` does: their changesets stay for a later run, and
a changeset naming an ignored package alongside one that isn't is an error. It
takes exact names and can't be combined with `ignore` in the config. Every
run, `--ignore` or not, refuses to skip a package a published package depends
on (not as a dev dependency) unless the dependent is skipped too, so nothing is
released against a dependency that stayed put.

Flags: `-n, --dry-run` (plan only), `--snapshot [tag]` and `--snapshot-template`
(`{tag}`/`{commit}`/`{datetime}`/`{timestamp}` suffix) for snapshot releases,
`--independent` to version each package separately instead of via a shared
version file, `-y, --yes` to accept the computed versions without the
interactive override prompt, `--no-stamp` to write nothing into any
manifest, `--changelog` to print each releasing package's changelog notes
instead of writing anything, and `--since <ref>` to narrow a preview
(`--changelog` or `--dry-run`) to what the branch adds, as `status --since`
does. A run that writes refuses `--since`: versioning only a branch's share
would drop the base branch's changes.

### Releasing at an exact version {#release-as}

On a terminal, `version` offers each releasing package's computed version and
lets you pick another. `--release-as` gives that answer up front, for CI or a
script:

```sh
changerig version --yes --release-as pkg-b=3.0.0   # this package at exactly 3.0.0
changerig version --yes --release-as 2.0.0         # the only version releasing
changerig version --changelog --release-as pkg-b=3.0.0   # preview it first
```

It's repeatable. The package has to be releasing already, from a changeset or a
commit: an override changes the number, not whether it ships. The version must
be valid semver above the current one. Packages sharing a version file move
together, as they do at the prompt, and the dependency cascade isn't
recomputed: dependents already in the release get the new version in their
ranges and changelogs, and an override that would push past the range of a
dependent that isn't releasing is refused, since nothing would update it. Give
that package a changeset with the bump you want instead, so the cascade runs. It's for normal releases only; a prerelease or snapshot sets its
own suffix. @changesets has no equivalent (there you write a changeset with
the bump you want), so without the flag nothing changes.

### Versions that do not live in the tree {#no-stamp}

Step 4 assumes the manifest is where the version lives. Two kinds of package
break that assumption:

- one whose version is **computed at build time** — MinVer reading git tags, a
  CI-stamped build — carries no number in the tree at all. Such a project is
  still discovered as a package (a `.NET` project is, when its effective
  `IsPackable` is true — or, when `IsPackable` is not set to false, when it
  declares a `PackageId` or references MinVer), listed as *no version in the
  tree*. The effective `IsPackable` is read across the project and its
  ancestor `Directory.Build.props` files in import order: the last
  unconditional assignment wins, as in MSBuild, and since conditions are not
  evaluated a conditional `true` anywhere counts as true — so a shared props
  file that sets it false for everything and true again under
  `Condition="…Contains('/src/')"` makes every project beneath it packable —
  while a conditional `false` is ignored. A MinVer reference counts whether it
  is a `PackageReference` in the project or a `GlobalPackageReference` in the
  nearest `Directory.Packages.props` (an outer one is read only where the
  nearer file imports it, as restore does). Commented-out elements are ignored
  throughout;
- one whose manifest is **not this repository's to write** — a member of a
  [stackspace](/rig/stack), whose directory leaves in pull requests to its
  upstream, where a stamped version would be a bump nobody asked for.

For both, `version` still computes the number, cascades it to dependents,
consumes the changesets and writes the changelog — and instead of stamping the
manifest it records the result in `.changeset/versions.json`. Discovery reads
that record as the package's current version from then on, so the next plan
bumps from it, and the release pipeline's `${version.<pkg>}` hands it to the
build (`-p:Version=` for dotnet, say). A stackspace member gets this without
asking; `--no-stamp` asks for it on one run, and `"versioning": { "stamp": false }`
in the config asks for it on every run. A member's changelog goes to the
stackspace root's `CHANGELOG.md`, one section per member, since its own
directory is not the stackspace's either.

A package with no version anywhere yet — nothing in the tree, nothing recorded —
plans from `0.0.0`; seed `.changeset/versions.json` with its real current
version, or type the exact version at the override prompt (or pass
`--release-as`), and it is remembered.
A package with no version in the tree is never stamped, whatever the config
says: a `<Version>` inserted into a MinVer project would fight the tool that
owns the number. The record and the manifest never disagree for long, either:
a later stamped run bumps from whichever is newer and, having written the
manifest, drops the record — so a one-off `--no-stamp` release is not repeated
by the next ordinary one.

Changelog generators are **pluggable** — the built-in renderer dogfoods the same
JSON contract external plugins speak. Set `"changelog": "<plugin>"` in config to
swap it in, or `"changelog": ["<plugin>", { … }]` to hand it options (as
@changesets does): the generator receives them as its request's `options`. A
bare name runs `changeset-changelog-<name>` from `$PATH`, and a path runs that
file. See [the plugin protocol](/core/plugin-protocol#changelog-generators).

### Links to commits and pull requests {#links}

The default changelog lists each change as its author wrote it. On GitHub,
`@changesets/changelog-github` is usually worth turning on: each entry links
the commit that added it and its pull request, and thanks its author.

```jsonc
{ "changelog": ["@changesets/changelog-github", { "repo": "acme/widgets" }] }
```

The commit comes from git (the commit that added the changeset, or a
commit-sourced change's own). The pull request and author are looked up with
the GitHub CLI, so the release job needs `gh` signed in (`GH_TOKEN` set to a
token that can read the repository, such as the workflow's `GITHUB_TOKEN`).
Without it, entries still link their commits. `@changesets/changelog-git` is
the lighter option: a commit hash on each line, no lookups. A GitHub
repository's `changerig init` offers the github layout for you (see
[`init`](#init)).

### A release record {#record}

Canon @changesets keeps no record of what it released: the current version is
whatever the manifest says, and a consumed changeset is the only trace of a
release. `"versioning": { "record": true }` keeps one, as release-please's
manifest does. Every `version` run writes the version each package it releases
lands at into `.changeset/versions.json`, under `released`, whether or not the
manifest is stamped:

```json
{
  "packages": {},
  "released": { "app": "1.0.1", "lib": "2.0.0" }
}
```

The record is never a version source. The manifest (or `packages`, for the
versions that don't live in the tree) stays that, so with changesets as the
source, turning it on changes no release decision. A snapshot records nothing, and nor does a range-only
rewrite, which releases nothing. A package enters the record the first time it
releases with the flag on; nothing is seeded, so the record never claims a
release that didn't go through `version`.

`doctor` checks the tree and the tags against it:

- a manifest whose version differs from the record was edited by hand, and the
  next plan would bump from it as if it had been released;
- a recorded release with no tag, for a package that has been tagged before,
  never finished publishing. It's expected in the window between the version
  PR's merge and the publish that tags it. A package that's never tagged is
  not flagged.

With commits as a versioning source, the record is also where each package's
next release starts counting. Without it, a package's commits count from its
highest release tag reachable from `HEAD` (a full version, `x.y.z` with any
prerelease; a .NET revision ranks before the prerelease), named as
the tag step names it: `lib@1.2.0`, a Go
module's `packages/lib/v1.2.0`, a single app's `v1.2.0`, or the `tagTemplate`
(one without `${name}`, like `v${version}`, is shared, so every package counts
from the latest). With it, a package's commits count from the commit that recorded its
current `released` version: the version PR's commit, or its squash on the
base branch. It's per package, so releasing `app` alone doesn't move `lib`'s
starting point, and it wins over a tag, which can be deleted or never pushed.
A release merged in from another branch counts from the commit that recorded
it there, not from the merge. The record has to be committed to count (it's
read from history, not the working tree), and a package it doesn't hold falls
back to its tag, as does one whose recorded version isn't its current one (a
release went out while the record was off). So does every package in a shallow clone, whose cut-off
history can't say which commit recorded a release; commit-sourced releases
want the full history anyway (`fetch-depth: 0` in GitHub Actions).

## `pre`

```sh
changerig pre enter next     # enter prerelease mode tagged "next" (1.2.0-next.0)
changerig pre exit           # leave prerelease mode; the next version is a normal release
```

Prerelease mode makes `version` produce tagged pre-releases (e.g. `-next.N`)
until you exit. The mode and tag are tracked in `.changeset/pre.json`; each
changeset a prerelease consumes moves into `.changeset/pre/`, and the `version`
after `pre exit` folds all of them into one stable release and removes both.
This is the @changesets v3 layout; a prerelease begun under v2 (consumed ids
listed in `pre.json`) is migrated on the next `version`.

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
