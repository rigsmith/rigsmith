# github.com/rigsmith/rigsmith

## 1.2.0
### Minor Changes

- Add `shiprig release --local`: run the full release pipeline for real but skip every network step (`publish`/`push`/`release`/`issues`), producing real local artifacts. Composes with `--only`/`--skip`/`--from`/`--to`; mutually exclusive with `--dry-run`/`--dry-build`.
  
- Velopack packaging is no longer .NET-only — the adapter now overlays **dotnet, cargo, node, and go**, releasing a `velopack.json`/`.jsonc` beside any of their manifests as a self-updating desktop app. `base` pins the ecosystem (else auto-detected); `build.command` builds the pack directory for non-dotnet bases (dotnet still auto-runs `dotnet publish`). Existing dotnet configs are unchanged.
  

### Patch Changes

- The release `build` step now inherits the run's resolved environment (`.env`/`.env.local` + ambient), so a desktop signer (Velopack/Tauri/Electron) gets secrets like `AZURE_*` straight from `.env.local` — no separate `source` or `signing.env` entry needed.
  
- `rig`'s dev-verb discovery no longer double-counts a project that has a Velopack (or Electron/Tauri) overlay file beside it. Overlay ecosystems re-emit their base-language project for the release path; surfacing them as dev targets produced a duplicate that, because `topoSort` keys by name, shadowed the real base target with an overlay copy that maps no `run`/`build`/`test` verb. The visible symptom: a configured `defaultProject` naming such an app "didn't match a runnable project", so a bare `rig run` opened the picker instead of launching it. Dev verbs now act only on the base ecosystem.
  
- `rig prune` now opens with a one-line banner — working directory, current branch, and primary-checkout-vs-worktree — so it's clear which repo you're tidying and that the current checkout is protected.
  
- The interactive `rig run` picker gains a `d` key that sets the highlighted project as the repo's `defaultProject` (so a bare `rig run` launches it without the picker), or clears it when pressed on the project that already is the default. The current default is marked with a green "★ default" tag in the list.
  
- Fix Velopack Windows packaging when cross-compiling from macOS/Linux: the adapter now prepends vpk's `[win]` directive and signs via a new host-aware `windows.signTemplate` (native Windows still uses `windows.trustedSigning`). `$VAR`s in the template expand from the build env, and `--storepass` tokens are redacted from echoed commands.
  

### Velopack

- Velopack: host-agnostic Windows signing, a real install DMG, and legible failures.
  
  - **Azure Trusted Signing now works from any host with no hand-written `signTemplate`.** When cross-compiling a Windows build from macOS/Linux, the adapter mints a Trusted Signing token from the `AZURE_*` service-principal creds in the build env and synthesizes the `jsign` command itself (RFC3161 timestamp + `--signExclude '\.dll$'` baked in). On Windows it still uses vpk's native `--azureTrustedSignFile`. A pre-set `AZURE_CODESIGN_TOKEN` is honored, and an explicit `signTemplate` still overrides. Missing creds now fail fast naming exactly which `AZURE_*` variable is absent, instead of an opaque signer error.
  - **macOS DMG is now a proper installer window** — the `.app` staged next to an `/Applications` symlink, arranged in icon view (drag-to-install), with a plain-symlink DMG fallback when Finder scripting is unavailable.
  - **The `version` step no longer fails for a project in a subdirectory.** The changerig version writer now populates `Package.Dir`, and the Velopack overlay falls back to the manifest's directory when `Dir` is empty — previously it resolved the base ecosystem at the repo root and errored.
  - **`0.0.0` no longer breaks `--dry-build`/`--only build`:** a skipped `version` step packs a valid `0.0.1` snapshot; a real build at `0.0.0` errors with guidance.
  - **Failures are legible.** Command errors now include the tool's stdout (not just stderr) — vpk writes its fatal line to stdout, so errors that read `exit status 255:` now carry the real reason. The release TUI's failure panel surfaces the failing command's output instead of only `step 'X' failed`.
  

## 1.1.0
### 🩹 Fixes

- Validate `add --package` names against the workspace, and drop ignored packages from the picker and the suggestion list
  

### Minor Changes

- Add `clauderig mv <src> <dst>` — move or rename a directory and relink its Claude Code history so the conversation stays attached. It renames the `~/.claude/projects` slug dir(s), rebases the cwd inside the transcripts, and updates the Desktop session metadata and settings additionalDirectories. Guards against moving a directory a live Claude session is running in, and against clobbering existing destination history. `--dry-run` previews; the move requires an interactive confirmation.
  
- `rig prune` now always shows why each worktree/branch was kept (the aligned name/state/reason table renders even when nothing is removable), and can force-remove kept items: `rig prune <name> --force` overrides a soft skip (unmerged, dirty, upstream-gone), and the confirm screen's `[f]` opens a checklist of forceable items. Hard rails still hold — the current, base, and primary checkouts can never be force-removed.
  
- rig custom commands now run cross-platform by default. A `.rig.json` shell-string command (e.g. `"lint": "eslint . && prettier --check ."`) executes through an in-process portable shell — pipes, `&&`/`||`, `$VAR`, globbing, and `cp/mv/rm/mkdir` all behave identically on Linux, macOS, and Windows, so per-OS `os.{macos,windows,linux}` variants are no longer needed just to be portable.
  
  Opt back into the OS shell with `"shell": "system"` (config-level, or per command) for scripts that need a real userland (`sed`, `awk`) or OS-specific syntax. Argv-form commands are unaffected (still exec'd directly).
  
  Behavior change: existing shell-string commands switch from `/bin/sh -c` / `cmd.exe /c` to the portable shell. The output and exit code are unchanged; only the interpreter differs. Set `"shell": "system"` if a command relied on a host-shell feature the portable shell doesn't provide.
  
- rig custom commands gain a Tengo `script` form for cross-platform command bodies with real logic. Alongside the shell-string and argv forms, a `.rig.json` command can now be a script:
  
  ```jsonc
  "commands": {
    "release": {
      "script": [
        "mkdir(`-p`, `dist`)",
        "log(`building for ` + ctx.os)",
        "if ctx.ecosystem == `go` { sh(`go build ./...`) }",
        "sh(`tar czf dist/app.tgz dist/app`)"
      ]
    },
    "clean": { "script": { "file": "./scripts/clean.tengo" } }
  }
  ```
  
  The script runs through the shared `core/script` runtime with `sh()`, `cp()`/`mv()`/`rm()`/`mkdir()`, `log()`, and `fail()` builtins — `sh()` and the file ops go through the portable shell by default, so the body is cross-platform (use `"shell": "system"` to opt `sh()` into the OS shell). A `ctx` object exposes `args`, `env`, `root`, `cwd`, `ecosystem`, and `os`. `--dry-run` previews side effects. Accepts a string, an array of lines, or `{ "file": "path.tengo" }` (resolved relative to the config; a non-`.tengo` extension still loads but is flagged as a likely typo).
  
- shiprig release: three ergonomics features that let custom pipelines (e.g. a local Velopack desktop release) stay declarative instead of falling back to hand-written shell.
  
  - **`${version}` is now the new (bumped) version**, resolved from the pending changesets at plan time — so it is correct in `--dry-run` and in every step, with no need to re-read the bumped value out of a manifest. Adds `${lastVersion}` (the pre-bump version) and `${nextVersion}` (an explicit alias of `${version}`), each with addressed (`${lastVersion.<pkg>}`) and aggregate (`${lastVersions}`/`${nextVersions}`) forms; also exposed on the script `ctx`.
  - **`commit.paths`** scopes the release commit to the listed paths (`git add -- <paths>`) instead of `git add -A`, keeping unrelated working-tree changes out of the release commit.
  - **Single-app repos default to the `vX.Y.Z` git tag.** A repo with exactly one discovered, non-Go package has no sibling name to disambiguate, so the tag now defaults to `vX.Y.Z` instead of `<name>@<version>`. (A repo with a second package — even an ignored one — stays on `<name>@<version>`.) **BREAKING** (treated as a minor for now): a single-package non-Go repo that was tagging `name@version` will switch to `vX.Y.Z` on its next release — set `tagTemplate: "${name}@${version}"` to keep the old tags. Go is unaffected (its `dir/vX.Y.Z` module-path tags are required for `go get`, and a root module already tags `vX.Y.Z`).
  - **`tagTemplate`** (changeset config) overrides the git tag for any repo, e.g. `"v${version}"` or `"${name}@${version}"`. Honored consistently by the tag, publish, and forge-release steps and the `${tag}` variable. Placeholders: `${version}`, `${name}`.
  
  See `examples/velopack-desktop/` for a worked configuration.
  
- Add a Velopack ecosystem adapter. A .NET project with a sibling `velopack.json` is now a first-class release unit: shiprig's `build` step runs `dotnet publish --self-contained` + `vpk pack` for each configured channel (RID), wraps the notarized macOS `.app` in a `.dmg`, and the `release` step attaches the installers **and the self-update feed** to the GitHub release — replacing both a hand-rolled `pack.sh` and a `release-github.sh`/`vpk upload` script.
  
  - **Overlays dotnet** (like Tauri overlays cargo): the adapter claims the `.csproj` next to a `velopack.json` and owns its build, while plain dotnet keeps packing ordinary libraries to NuGet. Version discovery and stamping delegate to the dotnet adapter, so csproj/`Directory.Build.props` handling is reused unchanged.
  - **Config in `velopack.json`** next to the project: `packId`, `channels` (RIDs), `mainExe`, `icon`, and per-OS signing (`macos.signIdentity`/`notaryProfile`, `windows.trustedSigning`). Signing secrets ride in through the existing signing-env seam, not the file.
  - **Host-aware**: macOS channels build only on a macOS host (signing/notarization/DMG); Windows/Linux channels cross-build anywhere. `--dry-build` (snapshot) builds everything unsigned for a fast rehearsal.
  - **vpk compatibility check**: the build fails fast if the installed `vpk` CLI major differs from the `Velopack` `<PackageReference>` the project pins.
  
  The update feed needs no `vpk upload`: Velopack's in-app updater finds updates by listing a release's assets over the GitHub REST API (the `releases.<channel>.json` index `vpk pack` produces, plus the `.nupkg` payloads named in it), so attaching those files to a published release via the generic forge step is a complete, working feed. The result is a fully native desktop release — no packaging or upload scripts.
  

### Patch Changes

- Embed per-tool icons and version metadata into the Windows .exe builds
  
- Fix the `tag` step never advancing a Go module past its first release. The gomod adapter treated the latest git tag as authoritative over the `// rigsmith:version` comment, so after `version` bumped the comment to the pending release, `shiprig tag` re-read the *previous* version from the existing tag and refused to create the new one ("0 tags, 1 already present"). It now takes the higher of the comment and the latest tag, so the comment — bumped ahead of the tag for a pending release — wins and the tag step creates `vX.Y.Z`. A released tag ahead of the comment is still authoritative.
  
- Extract the Tengo scripting runtime and the cross-platform portable shell into shared `core/script` and `core/shellrun` packages (previously private to shiprig's release pipeline), so other tools can reuse them. No behavior change for shiprig releases.
  

## 1.0.0
### Major Changes

- Initial release
