# github.com/rigsmith/rigsmith

## 1.8.0
### 🚀 Enhancements

- **rig:** new `rig stack` — repos you maintain forks of, fused into one git history so a change can span them in a single commit, while each still leaves as an ordinary pull request to its own upstream. `stack init` imports the repos your `rig.stack.jsonc` names, `stack pull` takes upstream's new commits into the right directory, and `stack send <repo> <name>` puts that project's changes on your fork as a branch holding one clean commit — no trace of the other projects. Send again to the same branch to update an open PR. Guide: https://rigsmith.dev/rig/stack
- **clauderig:** new `clauderig recent` lists your Claude Code sessions newest first — the answer to "I was working on it yesterday, what was it called?". Each line shows the client that ran it, the title, the branch it ended on and the project. `--since` (24h by default), `--until`, `--cwd` and `--account` narrow it, `-l` prints full ids with ready-to-run resume commands, and an optional search term narrows the window by title or body while keeping time order.
- **clauderig:** `clauderig desktop shortcut <name>` makes a clickable launcher for a Desktop profile — a `.app` bundle on macOS, a `.lnk` on Windows — on the desktop (`--to desktop`) or in `~/Applications` / the Start Menu (`--to apps`), with `--all` for every profile and `--rm` to take them away again. `desktop add` offers one at the end on a terminal (`--shortcut` / `--no-shortcut` to decide without being asked), and `desktop rm` deletes a profile's shortcuts along with it so no icon is left opening nothing.
  
  The shortcut runs `clauderig desktop open <name>` rather than launching Claude with the profile flag, so clicking it twice focuses the open window instead of starting a second instance on the same profile.
- **changerig:** changelog entries are now grouped by what a change *is* rather than by the order its file happened to sort in. Give a changeset a conventional type and scope — `feat(rig): …`, or `type:`/`scope:` frontmatter — and the type picks the section while the scope becomes the bullet's lead-in and groups that tool's entries together. `changerig add` infers the scope from the files your branch touched, `changelogScopes` in config sets which tool leads, and leaving the bump off a typed changeset lets the type decide it.

### 🩹 Fixes

- **rig:** `rig stack send` refuses when upstream has moved past your last `stack pull`. The workspace holds a snapshot taken at that pull, so committing it onto a newer tip would open a pull request that reverts whatever landed in between. Pull first, then send.
- **clauderig:** sessions are now dated by the last record inside the transcript rather than by the file's timestamp, which copying rewrites. A restore or a checkout of the synced repo used to re-date hundreds of old chats to today and bury the ones you actually used. Affects `recent`, `search` ordering and the `--since`/`--until` filters; a transcript with no dated record is marked `~` rather than guessed at.
- **clauderig:** `search` and `recent` now read every `clauderig desktop` profile rather than only the machine-wide install, and say which profile owns each session — so a Desktop session shows its title and which app to reopen it in. On a machine with three installs, two thirds of them were previously invisible.
- project discovery no longer walks into a linked git worktree. A worktree is a second checkout of the same repository, so discovering it returned a duplicate of every manifest — and a release could then act on the copy, writing its changelog into the worktree or building a tag out of the worktree's path. Submodules and nested clones are different repositories you put there deliberately, and stay discoverable; `rig --include-worktrees` still opts worktrees back in, and now reaches the walker rather than only the filter after it. The same rule applies to Node workspace globs and `rig copy`, which do their own traversal.

### 💅 Refactors

- The doctor presentation layer moved to `core/doctorui`, so code outside this module can render a `core/doctor` report and run the same fix-on-request flow. Nothing changes about how `rig`, `clauderig`, `changerig` or `shiprig` doctor behaves.

### 📦 Build

- Dependencies updated and the minimum Go toolchain raised to 1.26.7, clearing every `govulncheck` finding. Six were in the standard library, where no dependency bump can reach them — building from source now uses that toolchain, which Go switches to on its own. CI scans for known vulnerabilities on every change from here.

## 1.7.0
### Clauderig

- clauderig: `clauderig search` gains `--since`, `--until` and `--cwd` to narrow by when a session was last used and which project it ran in. Results now close with the device roster, and name any machine whose sessions the search could not see — a chat missing from a laptop that has not synced since Tuesday is no longer indistinguishable from one that never existed.
- clauderig: `sync` and `pull` now settle a diverged sync repo by themselves instead of stopping at the first conflict. Conflicts are resolved by policy — the clauderig manifest and device registry merge across machines, transcripts and memory notes keep both machines' additions, and machine-local state takes the newer snapshot — so a sync started by a hook or an agent no longer needs a terminal to finish. A staging repo left part-way through a merge by an earlier run is now repaired automatically instead of failing every session start with "unmerged files", and `clauderig doctor` reports that state as a `staging repo` failure it can fix.
- clauderig: sync now keeps a permanent record of every session it has staged, so a chat stays findable by title, project and date long after its transcript ages out of the synced window. `search` shows those as ledger results and says the body may still be recoverable from the sync repo's git history. Default transcript retention is now 90 days, up from 30.
- clauderig: new `clauderig ledger` command reports what the session record holds, and `clauderig ledger backfill` recovers entries for sessions that were pruned before the record existed, reading them out of the sync repo's git history. Run it once after upgrading — and before the repo next squashes its history, which prunes what backfill reads.

## 1.6.0
### 🚀 Enhancements

- `rig doctor` now reports on rig itself: which binary is running, the family on `PATH`, and what rig has registered in your shell.
  
  Doctor checked your *project's* environment — toolchains, per-project state, optional tools, `.rig.json` paths — and never checked rig's own. Two questions it couldn't answer:
  
  - Where is rig, and is there more than one? The toolchain rows say "not on your PATH" when a tool is missing but never report *which* binary answered, and nothing looked at rig at all. A second copy earlier on `PATH` is invisible until it answers a command you meant for the one you just upgraded.
  - What is actually wired into my shell? `rig setup` writes a marked block (the `rig()` wrapper that makes `rig cd` work, plus completions) and `rig alias install` writes another. rig wrote both and never read either back.
  
  ```
  Setup
    ✓ rig        1.4.2 · /Users/john/.local/bin/rig
    ✓ family     shiprig, changerig, clauderig · /Users/john/.local/bin
    ! shell      not installed in ~/.zshrc — `rig cd` can't change your directory and
                 tab completion is off; run `rig setup zsh`
    ✓ aliases    4 of 11 installed: rr, rb, rt, rcd · ~/.zshrc
  ```
  
  What warns is chosen so the group stays worth reading:
  
  - **Two copies on `PATH`** warns and names both, since the first is what runs. Running a binary that *isn't* on `PATH` doesn't warn — that's a source build or a `-dev` launcher, an ordinary dev loop — it just says what typing `rig` would run instead.
  - **The setup block missing or stale** warns: `rig cd` silently doesn't change directories and completion silently doesn't complete, so neither failure announces itself. A startup file holding only a `--dev` block is called out by name — that is the state behind "I ran setup and `rig cd` still does nothing", because the block that got installed binds `rig-dev`.
  - **The family and the aliases never warn.** Both are optional, and each companion's completion is loaded only when it's on `PATH`, so installing one later needs a new shell rather than a re-run. The rows exist to say what's there and where.
  - **A shell rig writes no snippet for** (`ksh`, an unset `$SHELL`) is reported as a fact about the shell, and the aliases row says "not checked" rather than claiming none are installed off a file it never read.
  
  The group runs before the ecosystem checks and, unlike them, runs everywhere: doctor used to return early with "no recognized projects found here — nothing to check", which skipped the whole report in exactly the directory you'd be standing in while asking why `rcd` does nothing. That directory now gets the Setup group and a note that only it ran.
  
  Nothing here is offered to `rig doctor --fix`. Splicing a block into your startup file is `rig setup`'s job, it asks first, and `--fix` doesn't — so the rows point at the command instead of running it for you.
  
  Also: **`rig alias list` marks the aliases that are actually installed.** It printed the fixed candidate set, identical on a machine with all eleven and one with none — so it answered "what could I install" while people were asking "why doesn't `rt` work". The mark comes from `installedAliases`, which already existed and was used in exactly one place: pre-checking boxes in the install checklist.
  
  ```
    ✓ rr   rig run  — Run the project
      rt   rig test  — Run the tests
    ✓ = installed in ~/.zshrc; `rig alias install` adds the rest
  ```
  
- `rig explain <verb>` prints what a verb resolves to — command, directory, environment, source — without running it.
  
  A `.rig.json` custom command can be silently wrong in a way nothing catches. Two real ones, live for weeks in a Chromium fork:
  
  ```jsonc
  "markers": "grep -rho 'sheepish-[a-z-]*' src | sort -u",
  "unowned": "grep -rho 'sheepish-[a-z-]*' src | grep -v '^./sheepish/' | sort -u"
  ```
  
  The first character class has no digits, so `sheepish-p3a-removal` was truncated to `sheepish-p` and counted as its own marker. The second pipes into a path filter that can never match, because `-h` upstream suppressed the filenames. Both exited 0 and printed a plausible list. Both were found by eye, long after the fact.
  
  Neither is a config error — the JSON is valid and the shell is valid — so no amount of validating `.rig.json` would have caught either. What was missing was a cheap way to look at the resolved command and think about it for ten seconds:
  
  ```
  $ rig explain markers
  Verb
    name:    markers
    source:  custom command · /repo/.rig.json
  
  Command
    runs:    grep -rho 'sheepish-[a-z-]*' src | sort -u
    shell:   portable · rig's in-process POSIX shell, same on every OS
    dir:     /repo
  
  Environment
    API_TOKEN=abc      · .env / .env.local
    SHEEPISH_ROOT=src  · .rig.json env
    the rest is inherited from the current environment
  
    nothing ran — `rig markers` runs it
  ```
  
  It covers every form a verb can take: the shell string after OS selection (with the shell that will interpret it — portable or the OS one, which decides what syntax works), the argv form (flagged as exec'd directly, since that is why a pipe in it would not do what it looks like), a Tengo script's source, a `package.json` script, a script directory, and the ecosystem conventions. Env is listed per variable with the layer that set it, and a `.env` value the ambient environment overrides is marked as such — the stated value is otherwise one the command never sees. Bare `rig explain` lists every verb the repo resolves.
  
  **Resolution is not reimplemented.** `customPlan`, `nodeScriptPlan`, `goScriptPlan` and `ecosystemPlan` are now the single resolvers, and the run paths call them: `runCustom` resolves then executes what came back. The env layers are named in one place and feed both the environment a command gets and the listing explain prints. A test asserts the two agree by comparing explain's output against what the run path echoes under `--dry-run` — an explain that can drift from a run is worse than no explain, since it would describe a command nobody executes.
  
  For the same reason, explain refuses rather than guesses. `coverage`, `rebuild`, `publish`, `outdated` and `upgrade` decide part of their command while running; an argument to a built-in verb selects a project or a test class at run time. In both cases explain says so, and where that verb's `--dry-run` prints an exact command it points there, since that goes through the real path. `info` and `outdated` have no such contract — one has no underlying command, the other runs its scans without echoing them — so explain says only that it cannot show a guaranteed answer.
  
  Also in this change:
  
  - **A `commands` entry shadowed by a built-in verb now warns on load.** Defining `"build"` in a Node repo was silently ignored: the built-in ran, the config appeared accepted, and the symptom read as rig malfunctioning rather than as a naming collision. It now says `"build" in /repo/.rig.json is a built-in rig verb, so that entry never runs — rename it (e.g. "build:custom")`, and `rig explain build` repeats it next to the verb, which is where someone asking the question will be.
  - **Config parse warnings are surfaced at all.** A malformed file degraded to defaults, an unknown top-level key, a script file that wouldn't load — these were collected into `Config.Warnings` and then read by nobody, so a broken config looked like an accepted one. Some of them were good messages nobody had ever seen: an unknown key already came with `did you mean "commands"?` attached. They now print once per run, and `rig info` — the report about the config, and where you look when rig isn't doing what the config says — grows a `Warnings` section listing the same problems. Commands that report them in their own output (`info`, `explain`) skip the per-run notice rather than saying it twice, and completion requests are exempt because a shell parses that output.
  
- `rig verify` runs build → test → run in sequence, then proves the artifacts were built together.
  
  `build`, `test` and `run` each produce or consume artifacts, and nothing checked that the ones in play were produced *together*. Every verb answers its own question honestly and the answers are still collectively wrong. On a Chromium fork: `rig tests` built only the unit tests (the underlying script takes one comma-separated `--target`, the config passed the flag twice, so the first target was silently dropped), then ran a browser-test binary that was two hours old, while a resource change had renumbered string IDs and regenerated the `.pak` files without relinking it. Every browser test crashed in a feature nobody had touched. Then the same shape from the other side: `rig start` launched a bundle loading a fresh dylib beside a stale `.pak`, and the crash read "invalid extension manifest". Three verbs, three green results, one broken product — and every stack trace pointed at innocent code.
  
  `rig verify` does the sequencing (stopping at the first failure, so "I checked" means one thing instead of three) and then the part that actually matters: it compares modification times to catch artifacts that disagree, *without* rebuilding to find out. Sequencing alone doesn't solve this, it hides it, by rebuilding everything every time — fine for a Go service, unusable where a build takes hours, which is exactly where stale artifacts survive longest.
  
  With no configuration it asks the generic question: is anything under the source tree newer than the newest build output? Output locations follow the ecosystem (Go `bin`/`dist`, Node `dist`/`build`/`out`/`.next` plus `node_modules` against its lockfile, .NET per-project `bin/<config>/<tfm>`, Cargo `target/<profile>`), and "source" is narrow enough that editing a README never reads as "you didn't rebuild". Artifacts rig cannot infer — generated resources, multi-artifact builds, an `out/` tree beside the repo — are declared in `.rig.json`:
  
  ```jsonc
  "artifacts": {
    "browser":    { "path": "../out/Component_arm64/Sheepish.app",
                    "inputs": ["**/*.cc", "**/*.grd", "**/*.gni"] },
    "unit-tests": { "path": "../out/Component_arm64/brave_unit_tests",
                    "inputs": ["**/*.cc", "**/*.h"] }
  }
  ```
  
  `rig verify --stale-only` then answers "are the things I am about to trust built from the code I have?" in a second, against a build that takes two hours:
  
  ```
    ✓ build output  up to date with main.go
    ✗ browser       out/App.app/Contents/Resources/en.pak is 2h older than src/strings.grd (and 1 more file)
    ✗ unit-tests    out/unit_tests is 2h older than src/renderer.cc
  ```
  
  A directory artifact is judged by its **oldest** file, which is the whole point — a bundle whose newest file is minutes old can still hold a resource the build never refreshed. Staleness exits non-zero (a warning in a long log is what got missed the first time), checks that couldn't run are reported as skipped rather than counted as passes, and a report where nothing could be checked says so instead of printing a green line. The run step passes by staying alive: a server never exits, so "still running after `--run-timeout`" (default 10s) is the answer to "does it start" — `--no-run` or `verify.run: false` drops it. It runs in its own process group, so the timeout takes down everything it started (`go run`'s compiled binary, a dev server's children) instead of leaving one behind holding a port. Each step is the same `build`/`test`/`run` command the root tree registers, because a `verify` that could disagree with the verbs about what it ran would be worse than no `verify` at all.
  
- An unknown flag now names itself and hands back the `--` form of your own command line.
  
  `rig build --target=brave_browser_tests` in a Node repo ended at `Unknown flag: --target.` and nothing else. The flag was named — pflag has always done that — but the way out wasn't: rig forwards anything after `--` to the ecosystem command, and no error, no `--help` screen and no page of the docs said so. The escape hatch existed and was unreachable except by guessing.
  
  The error now answers both questions it used to leave open — was that a typo, and how do I pass it through:
  
  ```
    ERROR
  
    Unknown flag: --target.
  
    rig build doesn't take --target. To pass it to the underlying command, put it after --:
  
        rig build -- --target=brave_browser_tests
  ```
  
  The suggested line is the user's real command line with `--` inserted at the flag that failed, not a generic example, so it can be pasted as typed — quoting included, and with the tokens rig did understand left in front of the separator (`rig build -aZ` suggests `rig build -a -- -Z`, keeping `-a` as `--all`). Inserting at the *first* unrecognized flag means one edit fixes a line carrying several of them.
  
  Unknown flags still error rather than forwarding automatically. Forwarding would mean `rig build --dry-runn` silently runs a build with no dry run, and rig's own flags would become the special case needing an escape hatch instead of the ecosystem's. The cost of erroring was never the rule, it was that the rule was undiscoverable — so a near-miss of a real flag is now called out as one (`Did you mean --dry-run?`), and the passthrough form is offered alongside it in case it wasn't a typo. The budget for "near miss" scales with the flag's length: two edits over a short name is a different flag, not a typo of one, and a confident wrong guess is worse than none.
  
  Applies to every verb that appends what it doesn't consume to the ecosystem command — `build`, `test`, `run`, `format`, `lint`, `typecheck`, `clean`, `rebuild`, `install`, `ci`, `add`, `global`, `dlx`. A verb that assembles its own argv (`coverage`, `publish`, `worktree …`) keeps pflag's plain error rather than promising a `--` it cannot honour; those still get the did-you-mean. Each forwarding verb also states the convention in its own `--help`, so `--` is findable before you need it:
  
  ```
    EXAMPLES
  
      # flags rig doesn't own go to the underlying command after --
      rig build -- --verbose
  ```
  
  Two supporting fixes make the suggested line true wherever it is printed:
  
  - Args after `--` are no longer read as a selector. `rig test -- --logger=trx` in a .NET repo used to hand `--logger=trx` to the test-class matcher as if it were a class name; a verb now takes its project/class argument only from the tokens ahead of the separator.
  - `core/fang` renders a multi-line error with its layout intact — the headline as the error sentence, the rest unwrapped — instead of reflowing every line to the terminal width, which would break a command line the reader is meant to copy.
  

### 🩹 Fixes

- clauderig sync no longer aborts on a project memory/ symlink, and restore now recreates it: worktree slugs share memory with their main project via a symlinked directory, which the allowlist walker surfaced as a regular file — the copy then read a directory and the whole sync failed ("read …/memory: is a directory"). The walker now reports directory symlinks whose endpoints are both in the synced set, sync records them in the manifest (`links`), and restore recreates them — endpoints rewritten through the machine's slug map — whenever the target directory exists and nothing already occupies the link path. The shared memory itself still syncs once, under its canonical project slug.

### Changerig

- changerig: `status` and `version` now warn when a changeset names several packages but its body only talks about some of them — because one body is rendered verbatim into every changelog it names, so the packages you were not thinking about get an entry about something else. The warning names the packages that will get the text and suggests splitting; it never blocks a release, and stays quiet for packages in the same `fixed` or `linked` group, which share a body by design.
  

### Clauderig

- clauderig: stop syncing Cowork sandbox contents from the Desktop root. A `local_<id>/` directory under `local-agent-mode-sessions` is a session working directory — audit log, build outputs, an `.audit-key`, and the documents the user uploaded to that session — and was being carried to the sync remote wholesale. Only the `local_<id>.json` sidecar (the session metadata) syncs now. Sync also reconciles the staging tree against the allowlist on every run, so files an earlier, looser rule already staged are removed rather than left tracked and pushed forever — without that, tightening a rule only affects new files. Also fixes the Desktop `config.json` keep-filter, which retained a `preferences` key the app no longer writes and so synced an empty document; it now keeps `locale` and `userThemeMode` as well.
  
- clauderig: the worktree/base-branch guard now covers Claude Code's new `Monitor` tool, which runs shell commands like `Bash` and was slipping past unchecked. Installing hooks also brings an already-installed clauderig hook up to date instead of leaving it on an older release's tool list — run `clauderig doctor --fix` (or `clauderig project install`) to update an existing repo, which `doctor` now flags.
  
- clauderig: the `clauderig guide` CLAUDE.md block now says who a changeset entry is written for — the person reading the changelog, not the reviewer reading the diff. Run `clauderig guide install` to refresh the block.
  
- clauderig: `status` now reports which account the machine-wide Claude Code CLI is logged in as — the email, the plan, and the alias if one is set. On a machine tracking several accounts the live login changes and is otherwise invisible until something fails, which is exactly the state status should surface. It also flags the two ways that identity can be quietly wrong: a credential and profile block that disagree (the desync `account doctor` exists to catch), and a clauderig active pointer naming a different account, which makes the arrow in `account list` a lie. A login clauderig has never captured is called out too, since `switch` cannot return to it.
- clauderig: `status` and `doctor` now tell you whether your sync actually reached the remote. `last sync` reports the last local commit, so it stays green while pushes are being rejected — `status` adds an `unpushed` line and `doctor` a `pushed` check that fails when commits have never left the machine, pointing at the reconcile path when the remote has diverged.
  
- clauderig: `account` now states its scope — it switches the Claude Code CLI login only. Claude Desktop is a separate login with its own token store and its own claude.ai web session, and clauderig neither reads nor writes it, so a switch never signs Desktop in or out. `account list` says so, the `account` and `account switch` help say so, and docs/CLAUDERIG-ACCOUNTS.md records why moving Desktop's session is not something clauderig will do: Desktop signs in twice at moments a capture cannot see, Electron rewrites its config and holds its cookie DB open so writes underneath a running app are silently lost, and reading the session at all means driving a private Chromium sqlite schema.
- clauderig: `account switch` no longer logs the machine out. Switching to an account whose stored credential had been blanked (an expired refresh token, or a logout) wrote that blank over the live credential — it parses and writes like any healthy blob, so nothing stopped it, even though `list` and `doctor` were already flagging the account as `credential ✗ no tokens`. The switch now refuses and points at the repair. Also fixes two things found alongside it: the running-instance guard read session registry files that Claude Code 2.1.227 no longer writes, so it reported "nothing running" with a live session open and never refused anything — it now consults the process table, while still ignoring isolated `account run` sessions that a swap cannot affect; and repairing an account with `--from-session` no longer strips `organizationUuid` (which is the only field `doctor` can compare, so losing it turned the desync check into an unconditional all-clear) or `mcpOAuth` (the MCP servers' own logins).
  
- clauderig: account and Desktop ergonomics — `--json` on `account list`/`account switch`/`desktop list` (one object on stdout, human lines on stderr, refusals included so a script can branch on them); `account alias` for a short handle usable anywhere an id or email is, refused when it would shadow another account; `account disable`/`enable` to hold an account out of a bare `switch`'s rotation while keeping it switchable by name; and `account map`/`desktop map` binding a directory to an account and/or Desktop profile so a bare `account run` or `desktop open` there picks the right one, inheriting the nearest mapped ancestor. Mappings are per-machine, never synced, and dropped when their target is removed. The accounts screen gains alias editing and a disable toggle, and Claude Desktop profiles get an interactive screen of their own (also on the dashboard). Ergonomics credit claude-swap by realiti4.
- clauderig: `desktop add` now seeds a new profile from your existing Claude Desktop install, so it is usable immediately — MCP servers (`claude_desktop_config.json`), theme and locale come across, along with the small declarative config files. Nothing that carries the login does: `config.json` is rebuilt from the vetted portable keys rather than copied and filtered, so `oauth:tokenCache`, `oauth:tokenCacheV2` and `lastKnownAccountUuid` are absent by construction, and the session-state directories are never touched. A seeded profile still starts signed out. The vetted key list is now shared with `clauderig sync`'s config filter so the two cannot disagree about what is safe to copy. `--no-seed` starts from an empty profile.
- clauderig: `clauderig desktop` now walks you through setting profiles up — the ordered flow leads the group help, `desktop add` names the next step, and `open`/`quit` ask which profile instead of guessing when given no name.
  
- clauderig: understand Claude Code's per-profile Keychain entries (`Claude Code-credentials-<hash>`); refuse token-less credential round-trips; add `account add --from-session` repair; doctor now lists stored-account health
- clauderig: `clauderig sync` now backs up your Claude Desktop profiles — settings and chat history, never the login — staged as `desktop@<name>` with the same retention and redaction as the machine-wide install. `restore` recreates them on a machine that has never run `clauderig desktop`, each one signed out. Profiles follow the Desktop root's enabled flag, and `status` lists them. Removes `clauderig desktop share`, which pooled history between profiles by writing inside them.
  
  Also stops syncing `local-agent-mode-sessions/skills-plugin` — Desktop's own copy of the bundled docx/pptx/xlsx/pdf skills, 8 MB per profile that the app re-downloads on its own. Your next sync will drop it from the repo.
  
- clauderig: prune Desktop session sidecars whose transcript is gone. Transcripts were aged out of staging by mtime while their `claude-code-sessions` sidecars were never pruned at all, so the two trees drifted apart and staging accumulated titles for sessions that no longer existed. A staged sidecar is now retained exactly as long as the transcript it names, so retention drives both on one clock. Retention is deliberately NOT applied to a sidecar's own mtime — Desktop rewrites that on its own schedule, and sidecars a month old routinely name transcripts written days ago.
  
- clauderig: `desktop open` and `desktop quit` now resolve their target the same way — the profile you named, else the one bound to this directory, else a picker on a terminal (`quit` lists only the open windows), else an error naming both ways to say which. Neither picks for you. `quit` takes an optional name to match `open`, and reports "no profile windows are open" as a fact rather than an error.
- clauderig: new `clauderig desktop` command runs several Claude Desktop accounts side by side, each in its own permanent Electron profile, so they all stay logged in and their windows can be open together — `desktop add work`, then `desktop open work`. This is the opposite of the withdrawn session-switching feature and inherits none of its problems: nothing is captured, copied or decrypted, clauderig never reads Desktop's login, and each profile is a directory the app owns outright. macOS and Windows (where Anthropic ships Desktop); elsewhere it says so and points at `clauderig account`. Profiles live under ~/.clauderig, outside every sync root, so a logged-in session can never reach the sync remote — asserted by a test. Model and macOS launch mechanism credit guise by siddhjagani.
- clauderig: extend the secret tripwire to non-JSON files. Until now only parsed JSON was scanned, so a credential that wasn't JSON — Claude Desktop's `.audit-key`, an `id_rsa`, a stray `.pem` — synced untouched. Files are now judged on their name (key material is conclusive; `.npmrc`/`.env`/`.netrc` are confirmed against their content first) plus two unambiguous content rules, and a hit refuses the sync naming the file. Deliberately narrow: transcripts are never entropy-scanned, because a tripwire hit aborts the whole sync and a false positive there would stop syncing entirely.
  
- clauderig: `account switch` now holds Claude Code's own credential locks while it swaps. Claude Code guards its token refresh with two advisory locks — `<config-home>/.oauth_refresh.lock` and the legacy `~/.claude.lock` — and refreshes by reading the credential, making a network round trip, then saving; a swap landing inside that window was overwritten by the refreshed *old* account's token, leaving the machine authenticating as the account it had just left. Under the lock, Claude Code's own re-read sees the swapped credential and abandons the refresh. Two more capture-path fixes alongside it: `account add` refuses to run inside an isolated session profile (`CLAUDE_CONFIG_DIR`/`CLAUDE_SECURESTORAGE_CONFIG_DIR`), where it would have stored the machine-wide credential under the session account's name; and atomic writes now resolve a symlinked `~/.claude.json` instead of replacing the link with a regular file, which silently detached it from a dotfiles repo. Lock protocol and both fixes credit claude-swap (realiti4, MIT) PRs #167, #190/#205 and #201; the protocol was re-verified against the Claude Code 2.1.227 bundle.

### Rig

- rig: `rig explain` now describes what the run path will actually do. A Go module whose mains live under `cmd/` no longer gets `go run .`, and the `--dry-run` suggestion places the flag before any `--` so it enables dry-run instead of being forwarded to your program.
  

## 1.5.2
### 🩹 Fixes

- Running `changerig` or `shiprig` outside a configured repo now exits 0 with guidance, instead of exiting 1.
  
  Both route their bare invocation to `add` and `status`, which need a configured workspace, so merely running the binary anywhere else failed — while `rig` and `clauderig` exit 0 from the same place. Anything that probes an installed CLI sees that first: winget's validation VM runs the executable after installing a package, records the non-zero exit against the install, and labels the submission `Validation-Executable-Error`. It did so for ChangeRig in 1.4.0 (16 days to merge) and again in 1.5.1.
  
  Only a genuinely bare run is softened. `changerig -m "…"`, `shiprig status`, and the `add`/`status` subcommands invoked by name all still exit non-zero in an unconfigured repo — a CI gate calls `status` explicitly, and that contract is unchanged and now covered by its own test. The guidance text is identical either way; only the exit code moved.
  
- `clauderig`'s description no longer makes Windows tooling read the binary as an installer.
  
  komac decides whether a `.exe` inside a zip is an installer or a portable binary by substring-matching its PE `FileDescription` and `OriginalFilename` against `["installer", "setup", "7zs.sfx", "7zsd.sfx"]`. Nothing else about the binary is considered. clauderig's description read *"Sync your Claude Code **setup** across machines"*, so komac wrote `NestedInstallerType: exe`, winget unpacked the zip and put nothing on PATH, and the failure surfaced only as a moderator asking "Is this a Portable package?" — 23 days after the ClaudeRig 1.4.0 submission, and again on 1.5.1.
  
  It was never a quirk of the binary. `rig`, `shiprig` and `changerig` were always detected correctly, and `rig` actually contains *more* installer-related strings than clauderig; the only difference was one word in a description. Reworded to "configuration", and verified by rebuilding the Windows binary and re-running the analyzer: `NestedInstallerType: portable`.
  
  A test in `build/winres/` now fails if any tool's `FileDescription` or `OriginalFilename` picks up one of those words. It is deliberately stricter than komac, which matches case-sensitively — a capitalised "Setup" would slip past komac today, but it reads as an installer to a human and to any future tightening of that check. The correction step in `winget-submit.sh` stays as a regression detector: it now warns loudly rather than quietly fixing, because after this it should never fire.
  
- winget submissions go through komac again, so a version bump stops dropping half the package's metadata.
  
  All five 1.5.1 submissions drew `Manifest-Metadata-Consistency`: `PublisherSupportUrl`, `Copyright`, `Tags`, `ReleaseNotes`, `ReleaseNotesUrl` and `Commands` had all vanished compared to the published 1.4.0 manifests, and `Moniker` had regressed from `changerig` to `ChangeRig`. The cause is structural rather than a missing setting — GoReleaser writes each manifest from its own config, so whatever it has no field for is gone, while komac updates the *published* manifest and rewrites only the version, URLs, hashes and notes. `Commands` — what `winget search` and `winget install --command` read — has no GoReleaser field at all.
  
  `scripts/winget-submit.sh <version> [--submit]` now owns the lane: generate with komac, correct, verify, then submit. GoReleaser's winget publisher is disabled, its config kept so switching back is one line per package.
  
  The correction step is not incidental. komac analyses each installer instead of trusting the published manifest, and reads `clauderig.exe` as an installer — writing `NestedInstallerType: exe` for that one package while getting the other four right. That is the same misdetection that shipped in ClaudeRig 1.4.0 and returned 23 days later as a moderator asking "Is this a Portable package?". It is corrected and logged before submission.
  
  Because komac submits a directory as a separate step, `check-winget-manifests.sh` now runs **before** anything reaches winget-pkgs — a real gate, where every earlier version could only fire after the PRs were open. It also learned two things about manifests it had been assuming: the keys may sit at the root (komac) or inside each `Installers:` entry (GoReleaser), and winget-pkgs files are CRLF, which defeats `$`-anchored patterns. It now requires `Commands` too, so this regression cannot repeat silently. Fixtures captured from both producers, byte for byte, cover all four shapes.
  

## 1.5.1
### 🩹 Fixes

- The npm wrapper packages can now be published from an already-published release, so one channel failing no longer needs a new version to fix.
  
  `node scripts/npm/build-packages.mjs --from-release v1.5.0 --publish` downloads that release's per-tool archives, verifies each against the release's own `checksums.txt`, unpacks the binaries and publishes the wrappers — the same bytes every other channel already carries, signatures intact. A `npm republish` workflow runs it with the registry token, taking a tag and an optional dry run.
  
  This exists because v1.5.0 published its GitHub release, Homebrew cask, Scoop manifest and five winget PRs, and then skipped npm: a manifest check placed before the npm step failed on a false positive and took the rest of the job with it. Recovering from a laptop was not an option — the wrappers are built from GoReleaser's `dist/`, which holds the *signed* binaries and exists only on the runner that built them, so a local rebuild would have pushed unsigned Windows binaries to npm. The alternative, cutting a fresh version to fix one channel, drags every other channel along with it, including five more winget PRs into a queue that already takes weeks.
  
  Verified end to end against the real v1.5.0 release: 24 binaries unpacked into 29 packages, and the Windows binary inside the built package still carries its Authenticode signature.
  
- `status` and `doctor` now name the changesets that can never release, instead of reporting "nothing to release" and leaving the contradiction to a human.
  
  A changeset whose front matter omits the package line parses cleanly, counts as pending, and is attributed to no package — so it contributes to no bump and no changelog, forever. Nothing surfaced that: `status` printed "Changesets found, but nothing to release" while holding sixteen of them, and `doctor` cheerfully reported "16 changeset(s)" pending. In this repo they accumulated across a full release cycle; 1.5.0 nearly shipped three weeks of merged work with no changelog entry, and the oldest stranded file had been sitting there since before 1.4.0.
  
  The tool already knew. It has the changesets and the package list, so it can say which file is inert and why — `status` now lists each one with its cause (names no package / names unknown package(s) / names only ignored package(s)) and, when exactly one package is releasable, prints the exact command that writes it correctly:
  
  ```
  Changesets found, but nothing to release.
  
    brave-lamps-hum      names no package
    lonely-otters-drift  names no package
    typo-comets-race     names unknown package(s): example.com/nope
  
  Name the package in the front matter — `changerig add -t <type> -p example.com/demo` writes it correctly.
  ```
  
  `doctor` gains a `changeset targets` check carrying the same finding as a warning, so it surfaces without having to run `status` and read past a line that says everything is fine.
  
  A changeset naming at least one releasable package is never reported, including one that also names ignored packages — the empty plan is not that file's doing.
  
- A contended identity-journal lock no longer fails on Windows — it waits its turn, as it always did on Unix.
  
  `clauderig`'s journal lock is an advisory file lock taken by exclusive create. Unix reports `EEXIST` when someone else holds it, which the retry loop expects. Windows reports `ERROR_ACCESS_DENIED` instead while the name is held by a file *pending deletion* — precisely the window another writer's release opens when it removes the lock. That isn't `EEXIST`, so it fell through to the fatal branch: two `clauderig` processes running at once could report `lock journal: Access is denied` rather than serializing, and the concurrent-writer test failed on roughly half of CI's Windows runs — including on commits whose diff was nothing but markdown.
  
  The retry loop now treats that spelling as "someone has this name right now" and keeps waiting. It stays Windows-only, so a genuine permission fault on Unix is still fatal immediately; and even on Windows it is safe, because the loop already gives up at its deadline and proceeds unguarded rather than blocking a diagnostic forever.
  
- The winget manifest check now reads the manifests GoReleaser actually writes, and runs last so it can't block a publish.
  
  Its first live run failed all five correct manifests. The check was written against the *published* manifest shape, where winget's publish pipeline has flattened the keys to column 0; in what GoReleaser generates they sit inside each entry of the `Installers:` list, indented. The `^NestedInstallerType: portable$` anchor therefore matched nothing, anywhere, ever — the check could only pass a manifest that would never exist.
  
  It cost more than a red tick. The step sat before the npm publish, so v1.5.0 published its GitHub release, Homebrew cask and five winget PRs, and then skipped npm entirely, leaving that channel a version behind a release that was otherwise fine.
  
  Two changes:
  
  - **It reads the real shape**, matching the keys wherever they are indented, and counts per installer rather than per file: a manifest with x64 portable and arm64 not is broken for half its users and used to pass. Fixtures are the bytes GoReleaser produced for the RigSmith.Rig 1.5.0 submission, captured verbatim, with the half-broken variant alongside — `go test ./scripts/` runs the script against both, so the check that gates a release is itself covered.
  - **It runs last.** The winget PRs are already open by the time it can look at them, so it is an alarm, not a gate: it has nothing left to prevent and everything left to break. A reporting check must not sit upstream of a publish.
  

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
