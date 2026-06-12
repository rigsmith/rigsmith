# rigsmith — questions for John

## Resolved (round 1, 2026-06-11)

1. **Repo layout** → monorepo confirmed. Kept split-friendly module paths.
2. **Go version model** → option (a) git-tag-native, (b) as secondary check. DONE:
   the gomod adapter now reads the latest matching git tag (`module/vX.Y.Z`,
   submodules `subdir/vX.Y.Z`) as authoritative, falling back to the optional
   `// rigsmith:version` comment. (Note: Go monorepos do NOT force one version —
   each submodule is independently tagged, so independent versioning is native.)
   `gitutil.ModuleTag` is ready for the `tag`/`publish` step to push tags.
3. **rig + relrig relationship** → separate binaries. DONE, plus a **third binary
   `changerig`** that isolates the changeset lifecycle (init/add/status/version/
   info) from the release orchestration. relrig re-exposes those + adds publish/
   tag/pre. `changeset` is wired as an alias on changerig for muscle memory (see #8).
4. **Dev-loop verbs as ecosystem-plugin methods** → yes. DONE: ecosystems now
   declare `DevCommands` in their `EcosystemInfo`; `rig` reads them instead of a
   hardcoded table, so adding a language adds its dev commands too.
5. **Config files** → allow all choices. Kept per-tool files for now
   (`.changeset/config.json` stays, for JS @changesets coexistence; `.rig.json`
   for rig). A unified `rigsmith.jsonc` can layer on later without breaking these.
6. **Charm stack** → cobra + fang + huh + lipgloss + bubbletea. Confirmed.
7. **Distribution** → yes. DONE (scaffold): `.goreleaser.yaml` (rig + relrig
   builds, changerig commented in), `scripts/install.sh` (curl|sh), and
   `docs/DISTRIBUTION.md`. `goreleaser check` not run (not installed locally).
8. **`changeset` muscle-memory alias** → DONE on `changerig` (`Aliases: [changeset]`).
9. **License** → MIT. DONE: LICENSE files at root + each module (John Campion Jr, 2026).

Plus from notes: **Rust ecosystem** added (`core/ecosystem/cargo`, full discover/
set-version, publish stubbed); **demo repo** at `examples/demo` (4 ecosystems);
**changelog generator plugins** wired (built-in dogfoods the contract; external
plugins resolve from config); **charm TUI** started (`ui` command, bubbletea menu);
**changelogen-style generator** shipped as a reference plugin (see new Q1).

## Resolved (round 2, 2026-06-11)

1. **Conventional types** → DONE. A changeset may carry a `type:` (e.g. `feat`,
   `fix!`) — explicit frontmatter or parsed from the summary's conventional
   prefix. **When a type is present the per-package bump is optional/omittable**
   (`"Name"` with no `: bump`), and the bump derives from the type via
   `changelogGroups` (configurable; conventional defaults built in). An explicit
   `: bump` is the override; `!` ⇒ breaking ⇒ major. Changelogs group by type
   section (built-in + the changelogen plugin both get the `type`). Proven e2e.
2. **relrig superset** → confirmed; kept.
3. **Discovery scope** → DONE per your answer: skip the usual junk dirs +
   `.gitignore`d files (new `core/walkutil`), whole repo by default, and a
   `paths: []` config narrows it.
4. **Carryover engine** ("go for it") → started: the `tag` command is now real
   (git-tag native for Go: `dir/vX.Y.Z`; `name@version` elsewhere; skips existing).
   Remaining: pre/snapshot modes, publish delegation, npm range-aware cascade.

## Resolved (round 3, 2026-06-11)

- **rig polyglot `primary`** (was Q-A) → DONE: `rig` resolves the primary by the
  **nearest manifest walking up from cwd**; `.rig.json` `"ecosystem"` overrides.
  When two ecosystems sit at the **same** level it **stops** with
  `Multiple ecosystems found here (dotnet, node) — set "ecosystem" in .rig.json`.
- **Range-aware cascade** (was Q-B, "knock out first") → DONE: new
  `semver.Satisfies` (caret/tilde/comparators/x-ranges/`||`/`workspace:`); the
  planner cascade now releases a dependent only when a dep's new version is **out
  of range** (rangeless refs always cascade, as before), bumps by the
  `updateInternalDependencies` threshold, forces **major** for a peer dep on a
  minor/major release, and **devDependencies** rewrite the range without a
  release. Manifest ranges are rewritten (`^1.0.0`→`^2.0.0`). Proven e2e on an npm
  workspace; 5 new planner tests + ~90 semver-range tests.
- **changelogGroups configurable** (was Q-C) → confirmed already configurable
  (config `changelogGroups` overrides the built-in changelogen-style defaults).

## New / follow-up questions (round 2 — A & B now resolved above)

### A. Polyglot `primary` ecosystem for `rig`
With recursive discovery, a repo that contains more than one ecosystem (e.g. the
rigsmith repo now contains the 4-ecosystem `examples/demo`) makes `rig`'s
`primary` resolve to whichever adapter is first in registry order (dotnet today).
So `rig info` at the rigsmith root says `primary: dotnet` even though rigsmith is
a Go project.
- **Question:** how should `rig` pick the primary in a polyglot repo — nearest
  manifest to cwd, a `.rig.json` setting, or the ecosystem at the repo root
  specifically? (I'd make it: ecosystem owning the cwd/nearest manifest, with a
  `.rig.json` override.)

### B. Engine sequencing (carryover)
Range-aware cascade: **DONE** (round 3). **Publish delegation: DONE** (round 4) —
`relrig publish` discovers, publishes each package to its registry (dotnet
pack + `nuget push --skip-duplicate`; `npm publish` guarded by `npm view`;
`cargo publish` with already-published detection; Go = git-tag), then creates +
pushes a tag per package. Idempotent; `--dry-run` is uniform and toolchain-free;
`--no-git-tag` / `--no-push` / `--access` flags. Only **pre/snapshot modes**
remain in the core release loop.
- **One thing to confirm:** publish does real network side-effects (registry
  pushes; tag push when a remote exists). It's idempotent and `--dry-run` safe,
  and non-interactive (CI-friendly) right now. Want an explicit confirm prompt
  before the first real push, or keep it non-interactive? (I left it
  non-interactive; a `--yes`/confirm gate is a quick add if you prefer.)

### C. changelogGroups defaults
The built-in default groups are changelogen-flavored (feat→🚀 Enhancements,
fix→🩹 Fixes, perf→🔥 Performance, …, breaking→💥 Breaking Changes). Happy with
that palette/wording as the default, or want a different default set?
