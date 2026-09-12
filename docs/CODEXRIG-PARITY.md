# codexRig vs claudeRig — what is built, what is not

Every claudeRig feature, and where codexRig stands against it, as of the first
implementation.

Read the **State** column as:

| | |
|---|---|
| ✅ | built, and doing the same job |
| 🟡 | built, but narrower than clauderig's — the Notes say how |
| ⬜ | not built; a real gap |
| ➖ | no Codex equivalent exists, so there is nothing to build |

A deliberate difference is marked 🟡 or ➖ with the reason, not ✅. The point of
this table is to be usable as a to-do list, which it cannot be if the awkward
rows are rounded up.

## Commands

| claudeRig | codexRig | State | Notes |
|---|---|---|---|
| `init` | `init` | ✅ | Same interactive flow. Adds a "carry sessions too?" question, since that is opt-in here, and prints what is and is not covered. |
| `sync` | `sync` | ✅ | `--dry-run`, `--hook`, `--flush` all present. The flush payload reads `rollout_path`/`transcript_path`/`session_file`, since Codex names it differently per event. |
| `pull` | `pull` | ✅ | Never fatal, same as clauderig: a session must start whether or not the network is up. |
| `restore` | `restore` | ✅ | `--dir`, `--force`, `--prune`. Also names the config files whose comments a rewrite cost, which clauderig has no need to do. |
| `status` | `status` | ✅ | Same `--json` shape idea: the gathered struct plus a level, a stable reason token and an action. |
| `doctor` | `doctor` | ✅ | `--fix` and the same non-zero exit. Extra check clauderig cannot have: whether Codex will actually run the hooks. |
| `config` | `config` | ✅ | `show`/`get`/`set`/`path`/`edit`. Keys differ where the tools differ; `syncSessions` is new, `chunkTranscripts` is absent. |
| `guard` | `guard` | 🟡 | Base-branch and hidden-worktree protection, and a patch is judged whole. No session-relocation refusals: Codex has no such tools. No commit inspection — the write is refused before the commit, which is the better moment. |
| `guide` | `guide` | ✅ | `AGENTS.md` instead of `CLAUDE.md`. `install`/`uninstall`/`status`/`show`, same managed-block mechanics. |
| `search` | `search` | 🟡 | Grouped by session, with `--since`/`--until`/`--cwd`/`--live`/`--repo`/`--json`. No `--raw`/`--all` grep mode, and no ledger rows for sessions whose bodies aged out. |
| `recent` | `recent` | ✅ | Plus `--json`, which clauderig's lacks. |
| `mcp` | `mcp` | 🟡 | Read-only, by choice: `codex mcp` already adds and removes, and a second writer for one TOML table is a way for the two to disagree. What this adds instead is a portability verdict per server. |
| `account` | `account` | 🟡 | `add`, `list`, `run`, `prepare`, `switch`, `sessions`, `remove`, `purge`, `doctor`, `alias`, `disable`/`enable`. Missing `watch` and `map`/`unmap` (see below). |
| `global` / `hooks` | `global` / `hooks` | ✅ | Plus `trust`, which Codex requires and Claude Code does not. |
| `project` | `project` | ✅ | The guard hooks, in the repository's `.codex/`. |
| `local` | — | ➖ | Claude Code has `settings.local.json`, gitignored per checkout. Codex has no counterpart; a third scope would mean writing a file Codex does not read. |
| `ui` | `ui` | ✅ | Same intent-then-act dashboard. Its key legend is built from the action list, so an action cannot be added without its shortcut appearing — clauderig's lost one that way. |
| `merge` | — | 🟡 | The merge POLICY is built and runs automatically inside sync and pull. There is no separate `merge` command to drive it by hand. |
| `repo` (`gc`, `prune`) | — | ⬜ | No repo-size reporting, no manual repack, no history squash. Retention prunes rollouts, but nothing reports the git footprint or folds history. |
| `peek` | — | ⬜ | Reading another machine's session straight from the git object store, without restoring. A genuinely useful thing that is simply not built. |
| `ledger` | — | ⬜ | See "Session ledger" below. |
| `move` | — | ➖ | clauderig rewrites transcripts when a project directory moves, because Claude Code files them by a slug derived from the path. Codex files by date and records the directory inside the rollout, so there is no slug to rename — and rewriting the rollout is the one thing codexrig will not do. |
| `reroot` | — | ➖ | Same reason. `codex resume --cd` is Codex's own answer. |
| `desktop` | — | ⬜ | Codex's desktop host is the ChatGPT app, and no isolated account-profile launch has been established for it. The assessment said to treat this as separate work; it still is. |
| `device` | — | ⬜ | The device registry is written and read, but there is no command to list or forget a machine. |
| `account watch` | — | ⬜ | Polling for identity changes. Less useful here: Codex has no second identity store to drift against, so there is far less to watch. |
| `account map` / `unmap` | — | ⬜ | Binding a directory to an account, so a bare `account run` in that directory picks it. Worth building: clauderig's dirmap file format is already a documented integration point, and this store has no counterpart for it. |

## Subsystems

| claudeRig | codexRig | State | Notes |
|---|---|---|---|
| `redact` — secret scanner, tripwire, secret-preserving merge | shared | ✅ | Moved to `internal/agentrig/redact` and used by both. Two copies of a safety claim diverge. |
| `allowlist` — rule engine | shared | ✅ | Moved to `internal/agentrig/allowlist`. Each vendor supplies its own rule set. |
| `ghrepo` — private-remote gate | shared | ✅ | Moved to `internal/agentrig/ghrepo`; messages became tool-agnostic. |
| Allowlist rules | `internal/codexrig/allowlist` | ✅ | Codex's own, default-deny, with the dangerous exclusions written down even though default-deny covers them. |
| Format dispatch | `internal/codexrig/codec` | ✅ | A real codec seam — JSON and TOML — rather than clauderig's single suffix test. |
| Path rewriting (values) | via `core/pathmap` | ✅ | Same mechanism. |
| Path rewriting (**keys**) | `engine.PortablizeKeys` | ✅ | New, and necessary: Codex addresses per-project settings by path, so the path is a table key. clauderig has no equivalent because Claude Code has no such keys. |
| Incremental sync + stage clock | `engine` | ✅ | Same racy-mtime guard, measuring the source filesystem's granularity. |
| Large-file throttle | `engine.deferLarge` | ✅ | Same rule: past the threshold, restage only on a chunk's worth of growth or after a settle. |
| Per-file size cap | `engine` | ✅ | Same, including removing a copy an earlier uncapped sync staged. |
| Retention window | `engine` | ✅ | On copy and on the staged tree. The live file is never deleted. |
| Staged reconcile against a tightened allowlist | `engine.reconcileStagedRoot` | ✅ | Same. |
| Whole-tree publication audit | `engine.Audit` | ✅ | Same, plus: a file THIS run staged that the audit condemns is taken back out, which clauderig leaves in the tree. |
| Audit cache | `engine.auditCache` | ✅ | Same read-once-per-(size,mtime) contract. |
| Transcript scrubbing | `engine.scrubInto` | ✅ | Line-at-a-time, live file untouched, private-key material refused rather than rewritten. |
| **Chunked transcript storage** | — | ⬜ | clauderig splits a large transcript into content-addressed 4 MB parts, so an append costs one blob rather than a whole copy. Not built. A very long session costs more history than it needs to. |
| Manifest | `manifest` | 🟡 | Much smaller, because there are no slugs to translate. It carries the source OS, the Codex version, and a portable spelling of each working directory. |
| Device registry | `devices` | ✅ | Same shape; identity only, never a token. |
| Journal | `journal` | ✅ | One file per machine, append-before-commit, refused/failed/ok. |
| **Ledger** (permanent session index) | — | ⬜ | clauderig remembers a session after its body ages out, so a search can say "this existed, recover it from git history" rather than "no such conversation". Matters less while rollouts are opt-in; a real gap once they are on. |
| Merge policy | `mergepolicy` | 🟡 | Manifest union, device newest-per-machine, config newest-commit. A rollout merges only when one side is a PREFIX of the other; anything else is left for a person, per the assessment's instruction not to line-union divergent histories. |
| Git attribute hardening | — | ⬜ | clauderig's `backupgit` forces byte preservation against a hostile global `.gitattributes`. Not ported; a machine with aggressive CRLF settings could still mangle a staged file. **The most likely of these gaps to bite someone.** |
| Publish / reconcile / retry | `service` | ✅ | Tripwire inside the retry loop, merge repaired before capture, never `git add -A` over a conflicted index. |
| History squash | — | ⬜ | No `config-history` side branch, no size-triggered squash. |
| Hooks | `hooks` | ✅ | Plus trust, which Codex requires. Pinned by a live test against a real `codex`. |
| Settings auditing | — | ➖ | clauderig's `settings` package exists to catch Claude Code silently ignoring certain keys at certain scopes. No equivalent inventory is known for Codex; inventing one would be guessing. |
| Instruction blocks | `agentsmd` | ✅ | `AGENTS.md`, same marker mechanics, and a test asserting the prose still matches what the guard does. |
| MCP | `mcp` | 🟡 | Read-only, with a portability verdict. See the command row. |
| Guard | `guard` | 🟡 | See the command row. |
| Doctor | `doctor` | ✅ | Same `core/doctor` model and `--fix`. |
| Health / status | `status` | ✅ | Merged into one package; same "the worst true thing wins" priority order and stable reason tokens. |
| Account store | `account` | ✅ | Same layout, same `--json` contract, same refusal codes. |
| Account desync model | — | ➖ | Codex keeps identity inside the credential, so there is no second place for it to disagree with. Four faults are possible, and all four are reported. |
| Keychain handling | — | ➖ | Codex 0.144.6 has no OS credential store (`secret_auth_storage` is false). `auth.json` is the whole secret. |
| Credential lock cooperation | 🟡 | 🟡 | Claude Code takes a refresh lock that clauderig cooperates with. Codex takes none, so codexrig locks against ITSELF and protects against Codex by refusing while it is running, writing atomically, and keeping a backup. |
| Live-process detection | `account/live.go` | ✅ | Process table plus Codex's writer locks, with a process's home resolved against its own `HOME`. |
| Transcript/session reading | `rollout` | ✅ | A different format, read the same way: header from the front, activity from the tail, never the middle. Validated against every rollout on a real machine. |
| Session listing / search | `sessions` | 🟡 | Live and repo stores, date-shard pruning. No ledger rows, no duplicate/split detection, no Desktop sidecars. |
| Split-session health | — | ⬜ | clauderig detects one session filed in two places and can consolidate. Codex's date sharding arguably makes this MORE likely — a session resumed the next day plausibly writes under a new date — so this is worth building. |
| `dirmap` | — | ⬜ | Per-machine directory-to-account bindings. clauderig's `dir-map.json` is read by outside callers, so the format is settled; there is simply no codexrig equivalent yet. |
| `peek` | — | ⬜ | See the command row. |
| `contents` | — | ⬜ | "What is actually in my sync repo, by category and size." |
| TUI dashboard | `tui` | ✅ | Same intent-then-act model. |
| Compatibility fixtures (pinned-baseline differ) | — | ⬜ | clauderig builds a shipped baseline binary and diffs its behaviour. A new tool has no baseline; the pattern is worth adopting from the first release rather than retrofitting. |
| End-to-end suite | 🟡 | 🟡 | Round-trip, cross-machine restore, tripwire, retention and scrubbing are covered as engine tests against synthetic homes. There is no gated `CODEXRIG_E2E` suite over a local bare remote. |

## Things codexRig has that claudeRig does not

Not parity, but worth recording — they came out of Codex being different.

| | |
|---|---|
| **Path-key rewriting** | Codex addresses settings by absolute path in table keys. Nothing in clauderig does this because nothing in Claude Code needs it. |
| **Hook trust** | `hooks trust` asks Codex for the hash and writes it through Codex's own config API. Claude Code has no trust gate. |
| **A codec seam** | JSON and TOML behind one interface, rather than a suffix test. This is the extraction the assessment asked for, done in the vendor that forced it. |
| **A portability verdict for MCP servers** | Which servers will work elsewhere, and what each one needs on arrival. |
| **Sessions as an explicit choice** | With every surface saying which mode the machine is in, so nobody assumes their conversations are backed up when they are not. |
| **Condemned staged copies are removed** | A file the audit finds a credential in is taken back out of the working tree, not just refused. |
| **`recent --json`** | clauderig's `recent` has no JSON output. |

## The order these are worth building in

1. **Git attribute hardening.** The only gap here that can silently corrupt data.
2. **`map`/`unmap` and the dirmap.** Small, and the file format is already settled by clauderig.
3. **Split-session detection.** Date sharding makes the two-copies case more likely, not less.
4. **The ledger**, before rollouts become the common case.
5. **Chunked rollout storage**, for anyone who turns sessions on and keeps them.
6. **`peek`, `repo`, `contents`, `device`** — useful, none of them load-bearing.
7. **Desktop profiles**, once an isolation mechanism exists to build on.
