# github.com/rigsmith/rigsmith

## 1.5.0
### 🚀 Enhancements

- `rig alias` installs short shell aliases for the verbs you type most — `rr` run, `rb` build, `rt` test, `rf` format, `rl` lint, `rcd` cd, `ri` install, `rup` upgrade, `rrm` uninstall, `rk` kill, `rw` watch. They're written to your shell startup file (zsh, bash, fish, or PowerShell) in their own marked block, separate from `rig setup`'s completion/cd block, so the two are managed independently: `rig alias install` / `rig alias remove` splice idempotently and never touch your setup block, and vice versa. Aliases are opt-in (they claim names in your shell's namespace) and named to avoid shadowing common commands — the uninstall alias is deliberately `rrm`, not `run`. `rcd` is rendered as a self-contained cd function (since `rig cd` can only print a path, not change the parent shell) that works with or without the setup wrapper. You choose which to install: on a terminal `rig alias install` shows a checklist, and `--only rb,rt,rcd` / `--all` pick without prompting; off a terminal (pipes, CI, `rig setup --aliases`) it takes the full set. Re-running is an edit — the checklist comes up pre-checked with what you already have, so unchecking an alias removes it and checking one adds it (the block is replaced with your current choices); uncheck everything to remove the block outright. The block always renders in canonical order so it stays diff-stable. `rig alias list` shows the set; `--print` inspects the snippet without writing. For a one-command setup, `rig setup --aliases` installs the shell integration and the aliases together, and a plain `rig setup` now points you at the flag.
  
- `shiprig release --channels` builds one target channel instead of the whole matrix. A Velopack app's `build` step packs every configured channel the host can handle — on a Mac that's both macOS RIDs *and* the cross-compiled Windows installer — so producing a single `.dmg` to hand someone meant editing `channels` in `velopack.json` and remembering to put it back. Now `shiprig release --channels osx-arm64 --dry-build` packs just the Apple-silicon installer (unsigned, for a local rehearsal), and `--channels osx-arm64 --only build` does the signed, notarized one. The flag takes a comma-separated list, matches case-insensitively, and builds in the order `velopack.json` declares regardless of how you type it; a name the config doesn't declare is an error listing the ones it does, so a typo can't quietly produce an empty build. Interactive releases don't need the flag at all: the plan editor grew a **Build channels** section beneath the steps, where `space` toggles a channel and `n` narrows to the one under the cursor (the last checked channel won't untoggle — an empty build isn't a state worth reaching). The list is discovered by dry-running the build, so it appears only for a release that actually has channels, and `--channels` preselects it when both are used. The underlying `channels` field is part of the adapter protocol's artifacts request and response, so an ecosystem with no channel concept simply ignores it.
  
- `clauderig account doctor` and `account watch` give a name to a failure mode that previously had no diagnostic at all. Claude Code's signed-in identity lives in two places written by independent code paths — the credential (the macOS Keychain, else `~/.claude/.credentials.json`) and the `oauthAccount` block in `~/.claude.json` — and nothing reconciles them. The server authenticates every request as the credential while every screen shows you the block, so once they drift apart, published artifacts, usage and rate limits land on an account the UI never names and no error is ever raised. Diagnosed from a live machine where the Keychain held one org for two weeks while the block named another. `doctor` prints both halves side by side and exits non-zero on a mismatch; `watch` polls and records each flip to `~/.clauderig/account-journal.jsonl` along with every Claude Code process alive at that instant, which is what makes attributing a change to its writer possible after the fact. Only identity-bearing fields count as a change — the file mtime and the process list are deliberately excluded from the fingerprint — so a poll loop can't bury real events under its own noise. Both commands are read-only and never write a credential. `add` and `list` run the same check and warn, since `list`'s `→` marks claudeRig's own pointer rather than proof of what the server sees.
  
  `doctor --fix` repairs a desync by rewriting the `oauthAccount` block from the stored profile of whichever account matches the live credential, and repointing `active.json` at it. The direction is the only safe one: the credential is what the server authenticates you as, so it is the truth, and the block is local belief — so the repair makes the belief honest and never touches the credential, meaning no running session is logged out. It makes the display honest about who you already are rather than choosing an account for you; `switch` remains the way to actually change account.
  
  `switch` is hardened to match: it now reads the target's stored `oauthAccount` block *before* any mutation and refuses when it's missing, instead of writing it best-effort after the credential had already moved. The old ordering meant that switching to an account with no stored block produced precisely this desync and still reported `Switched to …`.
  

### 🩹 Fixes

- Every ecosystem adapter now reports what its tool actually said when a command fails, whichever stream the tool chose.
  
  The .NET fix — errors built from stderr alone lose the diagnosis whenever a tool writes it to stdout — was not specific to .NET. `node`, `cargo`, `gomod`, `tauri` and `electron` all wrapped `strings.TrimSpace(stderr)` the same way, so any of them could reduce a real failure to an exit code and a colon. Meanwhile `velopack` had already hit this with `vpk` and fixed it locally, with a comment describing the identical `exit status 255:` symptom.
  
  That treatment is now one shared helper, `core/cmderr.Detail`: stderr plus the tail of stdout (bounded to 20 lines, so a build log can't bury the summary it ends with), or `(no output)` when a command fails silently. All seven adapters use it, and the two private copies are gone.
  
  Two nearby callers were deliberately left alone, because the pattern is only a bug where stdout carries diagnostics:
  
  - `core/auth`'s 1Password path — `op read`'s stdout is *the secret*. Appending it to an error would write a credential into a message that gets logged.
  - `core/plugin`'s subprocess host — stdout is the JSON protocol channel, not human output, and the plugin contract puts errors on stderr. It already falls back to the bare error rather than printing a dangling colon.
  
  The behaviour is pinned by tests on the shared helper (each stream alone, both together, the silent case, the line bound) plus the adapter-level wiring test.
  
- A lockstep group's release plan now lists every member, not just the ones carrying a changeset — the bump was already moving all of them.
  
  Packages that share a `Directory.Build.props` take their version from that one file, so writing a bump into it moves every member whether or not it has a changeset. Coordination only ever ran over the members that were already releasing, so the plan reported a subset of what the release did. In Avalite, one changeset on `Avalite.Core` printed a five-package plan and then moved all nine: `Avalite.Editing`, `Avalite.Icons`, `Avalite.IconTool` and `Avalite.Previews.Sdk` were versioned with no plan entry, no changelog, and would have been published at the new version.
  
  Lockstep now coordinates *and* adds the non-releasing members, the way fixed groups already do. Ignored packages are still skipped: the shared file moves their number, but `ignore` means "do not release this". The guard drops from "fewer than two releasing" to "none releasing", because with a shared version file a single releasing member is enough to move the whole group.
  
  Three tests cover it — the pull-in, the ignored-member exclusion, and the no-changesets case that must not manufacture a release. The first was verified to fail against the previous behaviour.
  
- `clauderig sync`'s secret tripwire no longer aborts the sync on two shapes it was misreading as credentials. Desktop names MCP tools `mcp__<server-uuid>__<tool>`, and the UUID escape hatch only recognized a UUID in *leading* position (`local_<uuid>`), so every approved MCP tool name in a Cowork session — and the matching entries in that session's `.claude/settings.local.json` — tripped as high-entropy. The entropy backstop now strips embedded UUIDs anywhere in the string and judges what's left, which covers both shapes; a blob that stays long and opaque after the UUID comes out still trips. npm/yarn lockfile `integrity` digests (`sha512-…`) are also exempt now: they're public content hashes of published packages, and they were tripping non-deterministically — only the ones whose base64 happened to contain no `/` got past the path filter, so a synced lockfile would fail on a random fifth of its entries.
  
- `shiprig publish` now loads `.env`/`.env.local`, so a registry key kept in a local `.env` reaches the push instead of the push going out with no credential.
  
  Only `release` and `init` ever called the env loader. A direct `shiprig publish` ran with the bare ambient environment, so the dotnet adapter's `NUGET_API_KEY` fallback (and core/auth's `env:NAME` references, and the OIDC context probe — all `os.Getenv` lookups) saw nothing: `dotnet nuget push` ran without `--api-key` and the feed rejected it. Exporting the same variable into the shell fixed it, which is what made this env loading rather than the key. The persistent `--no-env` flag was documented on `shiprig publish --help` as "skip .env/.env.local loading for this run" while that command never loaded any.
  
  `publish` now layers `.env`/`.env.local` under the ambient environment (a real `export` still wins) and *exports* the result for the run, before anything resolves a credential. Exporting rather than threading the map through `plugin.PublishRequest` is what the publish path needs: every credential lookup on it reads the process environment, and the adapters spawn their package manager with an inherited one — which is exactly why the same publish works under `shiprig release`, where it runs as a subprocess seeded with that environment. `--no-env` skips the file layer, making it a no-op.
  
  Auditing the other holders of the persistent flag turned up two more:
  
  - `shiprig doctor` probed `gh auth status` with the ambient environment only, so a `GH_TOKEN` declared in `.env` reported "not authenticated" while the release that check gates authenticated fine. It now probes with the layered view, honouring `--no-env`. It also *reports* an unreadable `.env` as a failure rather than quietly falling back to the ambient environment: `release` and `publish` both refuse to start on that error, so the one command whose job is to warn you should not be the one hiding it. The `gh` probe still runs, so a single bad file doesn't blank the rest of the report.
  - `rig`'s custom commands (`.rig.json` `commands`) loaded `.env` *regardless* of `--no-env` — the same flag inert in the opposite direction. Both env builders did it: `customEnv` for the shell/argv forms, and `customEnvMap` for the script form, where it feeds a Tengo script's `ctx.env` *and* the runner its `sh()` calls go through. They now share one file-layer reader that honours the flag, matching the built-in verbs.
  
  `shiprig tag` takes the flag too but creates local tags only — no credential, nothing for the layer to feed. The remaining subcommands (`add`, `status`, `version`, `info`, `config`, `packages`, `pre`, `ui`) are inherited from changerig and do no env-dependent work.
  
  Four tests cover it: an end-to-end `publish` run against a fake `dotnet` on PATH asserts the push carries `--api-key` from a key present only in `.env` (verified to fail on the old behaviour, with the reported keyless push), a `--no-env` run asserts it does not, and unit tests pin the export precedence and the `rig` custom-command flag across every command form.
  
- The .NET adapter no longer mistakes an `<ItemGroup>` item's `<Version>` metadata for the project's version — it was reading the wrong number, and the matching write path was corrupting the item.
  
  MSBuild spells item metadata with the same element syntax as a property, so a custom item can legitimately carry its own `<Version>`. Avalite's icon packs do exactly that: an `<IconPack>` item declares the pack's version alongside its name and source directory. The adapter matched `<Version>` anywhere in the document, so for a project whose `<ItemGroup>` precedes its `<PropertyGroup>` it reported the icon pack's `0.2.0` as the package version — visible in `shiprig info` as one package sitting at a version nothing had ever released.
  
  The write half was worse, because `writeVersion` targets "the same element `fromText` reads". A bump would have spliced the new version into the icon pack's metadata and left the project's own version untouched — silently changing a value that feeds the baked pack manifest, while the release appeared to succeed. Nothing about that surfaces until someone notices the pack version drifting with each release.
  
  Version reads and writes are now scoped to `<PropertyGroup>` blocks, so item metadata is invisible to both. The element-choice in `SetVersion` (`Version` wins over `VersionPrefix`) is scoped the same way, since an item's metadata should not decide which element a project bumps. A project whose only `<Version>` is item metadata is correctly treated as having no project version, so the bump is inserted into a `PropertyGroup` rather than hijacking the item.
  
  Three tests cover it — discovery, the in-place bump, and the insert-when-absent path — each verified to fail against the previous behaviour.
  
- `clauderig sync` no longer ages project memories out of the sync. Memory files
  (`~/.claude/projects/<slug>/memory/`) live under `projects/`, so the 30-day
  retention window applied to them exactly as it did to transcripts — a memory that
  hadn't been rewritten in a month stopped syncing and was then deleted from the
  staged tree, leaving a restored machine with a `MEMORY.md` index pointing at files
  it never received. Memory is durable state rather than a dated record, and a few KB
  per file, so it is now exempt from the window on both paths that enforce it: the
  copy-time cutoff and the staging prune. A project whose transcripts have all aged
  out also keeps its slug when it still has memory, so the memory keeps a home.
  
- rig prune now removes merged worktrees that contain submodules — git refuses a plain `worktree remove` on them ("working trees containing submodules cannot be moved or removed"), so prune force-removes worktrees it has already verified clean. The confirm screen also no longer swallows the per-item table when a run removes nothing, so a failed removal shows its reason instead of a bare "nothing to prune".
- A failing `dotnet` command now reports what went wrong instead of `exit status 1:` and nothing.
  
  The .NET adapter captured both streams and built its error from stderr alone. The dotnet CLI writes its diagnostics to **stdout** — a rejected push says `warn : No API Key was provided` and `error: Response status code does not indicate success: 403 (Forbidden).` there, with stderr empty — so the one thing that explained the failure was captured and discarded, and a 403 surfaced as an exit code, a colon, and blankness. Reproducing the command by hand was the only way to see the reason.
  
  Errors now carry stderr plus the tail of stdout, bounded to the last 20 lines so `dotnet pack`'s build log can't bury the summary it ends with, and a silent failure reads as `(no output)` rather than trailing off. This matches the treatment the velopack adapter already applies to `vpk`, which writes its fatal line to stdout for the same reason.
  
  Tests cover each stream in isolation, both together, the silent case, the bounding, and the end-to-end wiring through `runCmd` — the last using a sentinel split across `printf`'s format and argument so it exists only in the command's output and never in the argv the error echoes back. (Asserted the obvious way, that test passes with the fix backed out.)
  
- `clauderig sync` no longer syncs `node_modules`. A Cowork session that runs a build leaves its dependency tree inside the session's `outputs/` dir, which sits under an allowed tree — so tens of thousands of reinstallable files rode along with the session metadata (10 MB in one session here, plus the lockfile churn that was tripping the secret scanner). Allowlist rules gained an any-depth form, `**/name`, which matches that segment wherever it appears and prunes the whole subtree; specificity is now measured by how many path characters a rule pins down rather than raw pattern length, so a short any-depth exclude correctly outranks the long include it sits inside. Both roots exclude `**/node_modules`, covering session build output and skills with npm deps alike. Note that this only governs what future syncs stage: a `node_modules` tree already committed to your sync repo stays there until you remove it.
  
- `clauderig account`'s profile-block write is now atomic, and `doctor --fix` refuses an ambiguous repair. Both are follow-ups to the identity-desync tooling.
  
  Writing the `oauthAccount` block used `os.WriteFile`, which truncates the destination before writing. `~/.claude.json` holds far more than the identity block — project state, history, per-org caches, around 75 KB in practice — so a failure partway through would have left it truncated, and would also have made `switch`'s "credential rolled back, nothing changed" message untrue, since the credential would be restored while the profile stayed corrupt. The write now goes to a sibling temp file that is flushed and renamed over the destination, so the file is either wholly the old contents or wholly the new one, never a fragment. The destination's permissions are carried across rather than inherited from the temp file.
  
  `doctor --fix` located the account to repair from by organization and took the first match. Two logins can legitimately belong to the same organization, and a credential names only the organization — no email, no account uuid — so there was nothing to tell them apart, and the repair could have stamped one identity's profile block over another's. That is precisely the mislabeling the command exists to prevent, so it now refuses when more than one stored account claims the credential's organization, names the candidates, and points at `switch` or a fresh `add` to resolve it deliberately.
  
- `shiprig publish` and `shiprig tag` report each git tag once, instead of once per package sharing it.
  
  Both tagging loops iterated packages and rendered a tag for each. A `tagTemplate` like `v${version}` collapses every package in the repo onto one tag, so a 12-package repo printed `would tag v0.2.0 → push origin` twelve times for the single tag it would create. A real run was worse in a quieter way: one `tagged+pushed` followed by eleven `tag exists`, because each iteration re-read the tag the previous one had just created — reporting a tag it made as pre-existing. `shiprig tag`'s summary counted the same way, so "12 tag(s)" meant one.
  
  Both loops now skip a tag they have already handled this run, so the output has one line per git ref and the counts match what the run actually did. A tag left over from a *previous* run still reports `tag exists`, once.
  
  Covered by an end-to-end `publish --dry-run` over three packages sharing `v${version}`, asserting the tag is reported exactly once (three times before the fix).
  
- `clauderig sync` no longer lets one runaway transcript wedge the whole sync. A single marathon session can produce a `.jsonl` of several hundred MB — GitHub warns past 50 MB and refuses the push outright past 100 MB, so one such file took every other file down with it and the sync could never complete. Files above `retention.maxFileBytes` (default 50 MB, under the warning rather than at the cliff) are now dropped, and any copy an earlier uncapped sync had already staged is removed, so the cap can dig an existing repo out of the hole it was added to fix. Dropped files are named in the sync output rather than silently omitted — they're whole conversations, not incidental churn. A config predating the setting gets the default; disabling the cap takes an explicit negative value.
  
- The release workflow now verifies its own winget manifests, and a new script answers a moderator's questions before they're asked.
  
  `RigSmith.ClaudeRig` 1.4.0 took 23 days to merge because its manifest wasn't `NestedInstallerType: portable`: winget unpacked the zip and put nothing on PATH, automatic validation reported it as an unattended-install timeout (which reads like flakiness), and it took a human asking "Is this a Portable package?" to name it. Every pipeline we own was green the whole time.
  
  `scripts/check-winget-manifests.sh` runs after GoReleaser and fails the job unless every generated manifest is portable with a command alias per binary. GoReleaser opens the PRs before the check can run, so this is an alarm rather than a gate — but it fires in minutes instead of weeks, while the submission is still fresh enough to fix quietly.
  
  `scripts/winget-note.sh <version>` posts a short note on each open submission — portable, what lands on PATH, publisher, and that the binaries are signed. It previews by default (it comments on a third-party repo), skips PRs it has already noted, and exists because our submissions keep drawing `Policy-Test-1.2` manual review: ChangeRig 1.4.0 needed six waiver rounds.
  
  `docs/WINGET-SUBMISSIONS.md` records what the 1.4.0 batch actually cost and why — including that two of the five merged the same day, so winget is not uniformly slow for us — and the evidence against the obvious-but-wrong theory that the word "Claude" is what draws manual review (`RigSmith.ClaudeRig` has never tripped it; ChangeRig, with no brand word at all, tripped it twice).
  

## 1.4.0
### Minor Changes

- `rig build <name>` now disambiguates duplicate project names, just like `rig run`. When the same project is checked out in more than one location (e.g. a nested worktree) and its name matches several targets, `build` opens a picker on a TTY; off a TTY it lists the candidate paths and returns actionable guidance (narrow the name, run `rig build` from the target directory, or exclude the extra copies in `.rig.json`) — instead of silently falling through to the repo-root build command.
  

### Clauderig

- clauderig: `search` command finds Claude Code sessions by title or content across live ~/.claude and synced history, grouped into named sessions with ready `claude --resume` commands (--raw for grep lines, --all for every file); `restore`/`pull` now nudge you to restart Claude Desktop so recovered Code sessions reappear in the Code tab

## 1.3.0
### Minor Changes

- Install the whole toolchain with one command via a new combined `rigsmith` archive (all four binaries): `winget install RigSmith.Rigsmith`, `scoop install rigsmith`, `brew install --cask rigsmith/tap/rigsmith`, or `irm https://rigsmith.sh | iex`. Per-tool packages still ship. Also fixes the banner mojibake on legacy Windows consoles by switching the output code page to UTF-8 at startup.
  
- Add `shiprig release --rehearse`: a full dry run that touches neither git history nor the network. Shorthand for `--local --skip commit,tag` — version, build, and sign run for real while nothing is committed, tagged, or pushed. Like `--local`, it's a real run, so it can't combine with `--dry-run` or `--dry-build`.
  
- Surface duplicate-named projects instead of silently collapsing them by name (common with nested worktrees under `.claude/worktrees/`): `rig info` and `--all`/workspace views now list every copy with its path. `rig run <name>` matching several projects opens a picker (name · ecosystem · path) on a TTY, or lists the paths and errors actionably off one, rather than falling through to a misleading "Couldn't find a project to run." Name resolution also gained .NET dot-short matching (`App2` → `Tweed.App2`).
  

### Velopack

- Velopack: a new `macos.plist` config key feeds a custom `Info.plist` to `vpk pack --plist`, for bundle keys vpk doesn't generate (`NSServices`, `CFBundleURLTypes`, …). The adapter renders `${version}` to the release version before packing and drops `--bundleId` automatically (the plist supplies `CFBundleIdentifier`); `--icon` still applies. Omitting `macos.plist` keeps the current behavior.
  

## 1.2.1
### Velopack

- Velopack: the macOS install DMG now opens as a proper "drag to Applications" window (backdrop + arrow, app icon beside the Applications folder), and the mounted volume carries the app icon. The layout is written deterministically by building the `.DS_Store` directly instead of driving Finder, so builds are reproducible and work headless / in CI. Apps can override the built-in backdrop via `macos.dmgBackground` (and `macos.dmgWindow` for a HiDPI image's logical size).
  
- Velopack: re-packing the same version is now idempotent. The adapter clears that version+channel's existing nupkg(s) before `vpk pack` (prior versions stay, so delta generation still works), so vpk no longer fails with "a release equal or greater already exists" — `shiprig release --from build` resumes cleanly after a partial failure and local re-runs no longer need a manual `dist/releases` cleanup.
  

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
