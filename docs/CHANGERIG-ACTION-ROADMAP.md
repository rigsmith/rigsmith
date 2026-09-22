# changerig release action roadmap

Where the rigsmith release action (`.github/actions/release/`) and changerig can
go next, measured against the two tools it sits between: the official
`changesets/action` (whose flow it copies) and Google's `release-please`.

Written 2026-09-22, right after changerig moved to @changesets v3 parity (#441).
Nothing here is scheduled; it is a ranked list to pick from.

## How the two upstream tools differ

Both keep a standing release PR that is rebuilt on every push and cut the
release when it merges. They differ in where the version decision comes from.

| | changesets/action (v2, CLI v3) | release-please |
|---|---|---|
| Bump source | Changeset files authors add in their PRs | Conventional Commit messages (`feat:`, `fix:`, `!`) |
| Changelog text | Hand-written for readers, one entry per changeset | Generated from commit subjects |
| Release PR | "Version Packages", rebuilt from pending changesets | "chore: release …", rebuilt from commits since the last release |
| On merge | Runs your publish script; can create tags and GitHub Releases | Creates tags and GitHub Releases; registry publishing is your own step |
| Ecosystems | npm (`package.json`) only | Built-in strategies for Node, Python, Java, Go, Rust, Ruby, PHP, Helm, `simple` |
| Monorepos | Dependency cascade, `fixed`/`linked` groups, `ignore` | Manifest mode plus plugins (`node-workspace`, linked versions) |
| Prereleases | `pre enter` / `pre exit` | Prerelease settings in config |
| Forcing a version | Write a changeset with the bump you want | `Release-As:` commit footer or `release-as` config |
| Failure mode | A PR that forgets its changeset ships nothing | A sloppy commit message gives a wrong bump or a bad changelog line |

Notes:

- `changesets/action@v2` is required with CLI v3, and its inputs were renamed:
  `publish`→`publish-script`, `version`→`version-script`,
  `commit`→`commit-message`, `title`→`pr-title`.
- The action checks for pending changesets before calling `version`, which is
  why v3's "`version` exits 1 when there is nothing to do" is harmless in CI.

## Part 1: what changesets/action could learn from release-please

The pattern: release-please treats a release as **repo state it tracks**, while
changesets treats it as the consequence of files that happen to be present.
Items 2, 3, 7 and 9 all come from that difference.

The last column says where changerig and shiprig already stand.

| # | Idea | What release-please does | Gap in changesets/action | changerig / shiprig |
|---|---|---|---|---|
| 1 | More than npm | Version strategies for many ecosystems | Only `package.json`; everything else scripts around it | **Covered**: the multi-ecosystem engine is changerig's reason to exist |
| 2 | A record of what was released | `.release-please-manifest.json` plus `last-release-sha` / `bootstrap-sha` | Trusts whatever `package.json` says | Partial: `.changeset/versions.json` records versions that aren't stamped; no last-release commit |
| 3 | Publish only when the release PR merges | Label state machine: `autorelease: pending` → `tagged` | Runs publish on every push to main with no changesets and relies on "already published" | **Open**, same shape as ours (Part 2, item B) |
| 4 | Tags and GitHub Releases without a registry | Tagging and the GitHub Release are the release | Tags come from `changeset publish` / `git-tag`; private packages need `privatePackages.tag` | Mostly covered: shiprig's `tag` and `release` steps are separate from `publish` |
| 5 | Changelog sections by type | Features, Bug Fixes, … (`changelog-sections`) | Grouped only by Major/Minor/Patch | **Covered**: changelog groups by conventional type |
| 6 | Commit and PR links by default | On by default | Needs `@changesets/changelog-github` and a token | Partial: `changelog-github` supported, not the default |
| 7 | Forcing a version | `Release-As: x.y.z` footer, `release-as` config | None; you shape bumps to reach the version you want | Partial: interactive override prompt only (Part 2, item D) |
| 8 | Pre-1.0 behaviour | `bump-minor-pre-major`, `bump-patch-for-minor-pre-major` | A major on 0.x goes straight to 1.0.0, no switch | **Open**, and would have to diverge from canon (see below) |
| 9 | Separate or grouped release PRs | `separate-pull-requests`, grouping plugins | One PR releases everything pending | **Open** (Part 2, item E) |
| 10 | A fallback when authors forget | No per-PR step; every conventional commit counts | The bot only comments; a forgotten changeset ships nothing | Partial: `versioning.source: both` derives changesets from commits |

Items 3, 7, 8 and 9 would also make good upstream issues or discussions on
changesets itself. Since changerig matches canonical @changesets exactly
(including breaking upstream changes), a change to release **decisions**, like
item 8, belongs upstream first. Action-level features (3, 9) and opt-in flags
(7) can land here without breaking parity.

### Where changesets/action is already ahead: don't copy

- The dependency cascade and `fixed`/`linked` groups are far stronger than
  release-please's workspace plugins.
- Release notes are written on purpose by authors, not scraped from commit
  subjects.
- Its release PR body already shows each package's rendered changelog entries,
  as release-please's does.
- Deriving everything from commit messages as the default. It stays an opt-in
  (`versioning.source: commits`), which is the better arrangement.

## Part 2: gaps in our own release action

`release.sh` follows changesets/action closely. Ranked:

### A. Decide "is a release pending?" the way changerig does (bug)

The action counts top-level `.changeset/*.md` files. Two gaps:

- **Prerelease graduation, broken since #441.** Consumed prerelease changesets
  now live in `.changeset/pre/`. After `pre exit` with no new changesets the
  count is 0, so the action takes the publish path and never opens the PR that
  graduates the prerelease to stable.
- **Commit-sourced repos never get a version PR.** With `versioning.source:
  commits` or `both`, there are no files to count.

Fix: ask changerig, e.g. `changerig status --output plan.json` and check whether
`releases` is empty. This belongs with #441 or right after it.

### B. Publish only when the version PR merges

Today every push to main with no pending changesets runs publish and relies on
the registry checks to make it a no-op. Check first whether the merged commit
is the version PR (by label, as release-please does, or by commit subject).
That saves runs and makes "why did this publish?" easy to answer.

### C. Put the real release notes in the PR body

The body is `status` output in a code block. `changerig version --changelog`
already renders the exact entries without writing anything; use those, with
the plan as a summary line, so reviewers approve the words that ship. Same
script as A, so it's cheap to do together.

### D. Non-interactive version override

changerig's override prompt (`versionoverride.go`) can't be answered in CI. Add
a non-interactive form: a flag, or a `releaseAs` field in a changeset. Check
first whether the prompt already accepts a flag. It has to stay opt-in so
default behaviour keeps matching canon.

### E. A version PR per package, as an option

Let an app and a library release on different schedules (release-please's
`separate-pull-requests`). Matters most in polyglot repos like tweed.

## Suggested order

1. **A**, since it's a bug #441 introduced, together with **C** (same script).
2. **B**.
3. **D**, then **E**, when a repo actually needs them.
4. File upstream changesets issues for Part 1 items 3, 7, 8 and 9.
