# rigsmith/core

The shared engine behind `rig`, `changerig`, `shiprig` and `clauderig`.

**Stdlib only, with seven marked exceptions.** The release engine, and the
process/config/git packages under it, depend on nothing outside the standard
library — that's deliberate and worth keeping: it's the portable half, embeddable
anywhere without dragging a terminal stack along. The terminal chrome can't be,
so seven packages carry real dependencies and are marked † below: `brand`,
`climenu` and `doctorui` (lipgloss + huh), `fang` and `cliguard` (cobra), and
`script` and `shellrun` (Tengo, `mvdan.cc/sh`). Adding an eighth is a decision,
not a detail — everything else in the table is stdlib.

## Release engine

| Package | What |
|---|---|
| `semver` | SemVer 2.0.0 + node-semver bump rules + npm range matching |
| `changeset` | `.changeset/*.md` parse/render (shared @changesets format) |
| `commitsource` | conventional commits → in-memory changesets, so commit-based versioning is a second *source*, not a second engine |
| `config` | `.changeset/config.json` schema (changelog/format specs, ignore globs) |
| `planner` | release plan: range-aware cascade, linked/fixed/lockstep grouping, pre/snapshot |
| `changelog` | changelog-git/-github enrichment + release-line decoration + file writer |
| `prestate` | `.changeset/pre.json` prerelease state (shape shared with the JS tool) |
| `since` | maps files changed since a git ref → the packages and changesets they belong to |
| `issuerefs` | pulls `#123` / `KEY-123` refs out of released commits, closing vs. mention |
| `mdfmt` | native prettier-equivalent markdown formatter + `format:` dispatch |
| `plugin` | the extension contract (ecosystem adapters + changelog generators) |
| `ecosystem/{cargo,dotnet,gomod,node}` | built-in language adapters (reference impls of `plugin.Ecosystem`) |
| `ecosystem/{electron,tauri,velopack}` | desktop-app adapters, each overlaying a base language adapter |
| `ecosystem/regex` | generic version stamping for files that aren't a recognized manifest |
| `auth` | registry credential precedence: OIDC → secret ref (`op:`/`env:`/`cmd:`) → env var → ambient |
| `sign` | the same seam for a desktop build's signing/notarization secrets (opt-in) |

## CLI plumbing

Shared terminal surface, so the four binaries look and behave like one family.

| Package | What |
|---|---|
| `brand` † | the palette: brand colors + huh themes + fang color schemes, one accent per tool |
| `fang` † | vendored fork of charmbracelet/fang — help/usage/error styling, banner, groups |
| `climenu` † | bare cobra group on a TTY → interactive subcommand menu |
| `cliguard` † | asserts a cobra tree against the cross-tool CLI conventions (a lint for the command surface) |
| `doctor` | pure health-check model: each check reports OK/Warn/Fail/Info + an optional `Fix` |
| `doctorui` † | the terminal half of `doctor` — sectioned report, pre-checked multi-select, `--fix` |
| `cmderr` | failure detail from *both* streams, so a tool that errors on stdout still explains itself |
| `editor` | resolves which editor command to launch for an interactive edit |
| `match` | forgiving path-aware fuzzy ranking (exact > prefix > substring > subsequence) |

## Process, config + filesystem

| Package | What |
|---|---|
| `cfgfind` | resolves a tool's config across its allowed locations; refuses to guess when more than one matches |
| `confkit` | shared config mechanics: comment-preserving JSONC writer + truthy-env helper |
| `jsonc` | tolerant JSONC parse (byte-offset preserving) + comment-preserving editor |
| `envstack` | dependency-free `.env` reader + layered environment merging |
| `shellrun` † | cross-platform exec: direct runner, in-process portable shell, pure-Go cp/mv/rm/mkdir |
| `script` † | sandboxed Tengo runtime — `if` expressions and step scripts (shiprig steps, rig custom commands) |
| `walkutil` | the shared gitignore-aware walk every adapter uses to discover manifests |
| `copytree` | working-tree copy on walkutil's rules, `.git` skipped by default (`rig copy`) |
| `pathmap` | expands path templates for an *arbitrary target* OS — clauderig's cross-machine path correction |
| `dsstore` | reads/writes `.DS_Store`, minting a drag-to-install dmg layout headlessly (no Finder/AppleScript) |
| `devroute` | the per-repo active-worktree pin the generated `<tool>-dev` launchers build from |
| `gowork` | discovers the runnable tools under `cmd/` for the source and dev installers |

## Git

| Package | What |
|---|---|
| `gitutil` | tags, log, merge-base diffs — shells out to `git`, degrades when git or a repo is absent |
| `gitrepo` | the wider transport: worktrees, branches, and clauderig's sync staging repo |
| `worktree` | the worktree-layout convention (sibling directory, never nested) + review-window launcher |

---

`testdata/parity/` is the cross-implementation golden corpus (22 scenarios,
Node + C# oracles) — see its README for provenance and the regeneration rule.

```sh
go test ./...
```

See [../docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md) and
[../docs/PLUGIN-PROTOCOL.md](../docs/PLUGIN-PROTOCOL.md).
