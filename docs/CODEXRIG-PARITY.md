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
| `config` | `config` | ✅ | `show`/`get`/`set`/`path`/`edit`. Keys differ where the tools differ: `syncSessions` is new, and `chunkTranscripts` is `chunkRollouts`. |
| `guard` | `guard` | 🟡 | Base-branch and hidden-worktree protection, and a patch is judged whole. No session-relocation refusals: Codex has no such tools. No commit inspection — the write is refused before the commit, which is the better moment. |
| `guide` | `guide` | ✅ | `AGENTS.md` instead of `CLAUDE.md`. `install`/`uninstall`/`status`/`show`, same managed-block mechanics. |
| `search` | `search` | 🟡 | Grouped by session, with `--since`/`--until`/`--cwd`/`--live`/`--repo`/`--json`, and ledger rows for sessions whose bodies have aged out. No `--raw`/`--all` grep mode, and no `--account` filter: a Codex rollout does not record which login produced it, so sessions carry no account attribution to filter on. |
| `recent` | `recent` | ✅ | Plus `--json`, which clauderig's lacks. |
| `mcp` | `mcp` | 🟡 | Read-only, by choice: `codex mcp` already adds and removes, and a second writer for one TOML table is a way for the two to disagree. What this adds instead is a portability verdict per server. |
| `account` | `account` | 🟡 | `add`, `list`, `run`, `prepare`, `switch`, `sessions`, `remove`, `purge`, `doctor`, `alias`, `disable`/`enable`, `map`/`unmap`. Only `watch` is missing (see below). |
| `global` / `hooks` | `global` / `hooks` | ✅ | Plus `trust`, which Codex requires and Claude Code does not. |
| `project` | `project` | ✅ | The guard hooks, in the repository's `.codex/`. |
| `local` | — | ➖ | Claude Code has `settings.local.json`, gitignored per checkout. Codex has no counterpart; a third scope would mean writing a file Codex does not read. |
| `ui` | `ui` | ✅ | Same intent-then-act dashboard. Its key legend is built from the action list, so an action cannot be added without its shortcut appearing — clauderig's lost one that way. |
| `merge` | — | 🟡 | The merge POLICY is built and runs automatically inside sync and pull. There is no separate `merge` command to drive it by hand. |
| `repo` (`gc`, `prune`) | `repo` (`status`, `gc`) | 🟡 | Size by category, and a repack. No history squash: with chunking in place an append costs a chunk rather than a copy, so the growth squashing existed to fix is largely gone. |
| `peek` | `peek` | ✅ | `list`, `show`, `get`. Lists only what it can read — a log walk alone reports paths retention has since pruned — and `get` is additive, refusing rather than overwriting. |
| `ledger` | `ledger` | ✅ | What is remembered, and which machine recorded it. |
| `move` | — | ➖ | clauderig rewrites transcripts when a project directory moves, because Claude Code files them by a slug derived from the path. Codex files by date and records the directory inside the rollout, so there is no slug to rename — and rewriting the rollout is the one thing codexrig will not do. |
| `reroot` | — | ➖ | Same reason. `codex resume --cd` is Codex's own answer. |
| `desktop` | — | ⬜ | Codex's desktop host is the ChatGPT app, and no isolated account-profile launch has been established for it. The assessment said to treat this as separate work; it still is. |
| `device` | `device` | ✅ | `list` and `forget`. Forgetting a machine removes its entry only; what it synced stays. |
| `account watch` | — | ⬜ | Polling for identity changes. Less useful here: Codex has no second identity store to drift against, so there is far less to watch. |
| `account map` / `unmap` | `account map` / `unmap` | ✅ | Nearest binding wins. A bare reference with several accounts and no binding reports `unmapped-directory` rather than a generic failure. |

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
| **Chunked rollout storage** | `rolloutstore` | ✅ | Content-addressed 4 MiB parts plus a one-line index, past an 8 MiB threshold. Measured on the largest real rollout here — 172 MB becomes 44 parts and a 4 KB index, and one more turn reuses 43 of them. A chunked rollout is also exempt from the per-file cap, since no blob it produces is near a host's limit: without that, the biggest conversation on a machine is the one thing never backed up. |
| Manifest | `manifest` | 🟡 | Much smaller, because there are no slugs to translate. It carries the source OS, the Codex version, and a portable spelling of each working directory. |
| Device registry | `devices` | ✅ | Same shape; identity only, never a token. |
| Journal | `journal` | ✅ | One file per machine, append-before-commit, refused/failed/ok. |
| **Ledger** (permanent session index) | `ledger` | ✅ | One file per device, written before retention runs. `search` and `recent` surface remembered sessions with the git command that recovers the body; `codexrig ledger` reports what is kept. |
| Merge policy | `mergepolicy` | 🟡 | Manifest union, device newest-per-machine, config newest-commit. A rollout merges only when one side is a PREFIX of the other; anything else is left for a person, per the assessment's instruction not to line-union divergent histories. |
| Git attribute hardening | shared | ✅ | `backupgit` moved to `internal/agentrig` and is used by both. Written into the backup so it applies on another machine's first clone, `Prepare` on publish so a renormalised index cannot commit stale bytes, `Validate` on every push attempt. Proven by a gated end-to-end test with a control that fails if the hostile settings are not biting. |
| Publish / reconcile / retry | `service` | ✅ | Tripwire inside the retry loop, merge repaired before capture, never `git add -A` over a conflicted index. |
| History squash | — | 🟡 | No `config-history` side branch and no size-triggered squash. Much less pressing with chunking: the growth it existed to fix came from re-committing whole transcripts. |
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
| Session listing / search | `sessions` | ✅ | Live store, repo store and ledger rows, with one-sided date-shard pruning. The live store follows `CODEX_HOME`, so a session in an isolated account home is listed when that account is the one in use. No Desktop sidecars, because there are none. |
| Split-session health | — | ➖ | clauderig detects one session filed in two places and can consolidate, because Claude Code files by a slug derived from the working directory and a session that moves gets a second file. **Measured against 60 real rollouts: Codex never does this.** It APPENDS to the original rollout on resume, which keeps its original shard — one here spans eight calendar days with a single `session_meta`. The live-versus-repo case is handled by preferring the live copy, and the repo-versus-repo case by the merge policy. |
| `dirmap` | shared | ✅ | Moved to `internal/agentrig`. The path comparison is what is worth sharing, not the file format. |
| `peek` | `peek` | ✅ | See the command row. |
| `contents` | `contents` | ✅ | Behind `codexrig repo status`. |
| TUI dashboard | `tui` | ✅ | Same intent-then-act model. |
| Compatibility fixtures (pinned-baseline differ) | — | ⬜ | clauderig builds a shipped baseline binary and diffs its behaviour. A new tool has no baseline; the pattern is worth adopting from the first release rather than retrofitting. |
| End-to-end suite | `e2e` | ✅ | `CODEXRIG_E2E=1`: a full round trip over a local bare remote, byte preservation under hostile git settings in both line-ending flavours, and a refusal for a nested attribute file that would permit conversion. |

## What measuring Codex changed

Two entries above moved because real data disagreed with an assumption, and both
are worth naming rather than quietly editing.

**Split sessions are not a thing here.** The first version of this table said
date sharding made one session-in-two-places *more* likely. Sixty real rollouts
say otherwise: Codex appends to the original file on resume and leaves it in its
original shard.

**That same measurement found a bug in the session listing.** A rollout on this
machine spans eight calendar days from its shard date. The listing pruned
directories on both ends of a `--since`/`--until` window with one day of slack —
so `--since 2d` would have skipped the directory holding a conversation that was
active yesterday, and hidden exactly the long-running sessions somebody is most
likely to be looking for. The prune is now one-sided: only the late end, where a
session cannot have records from before it started.

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

## What is left

Four things. None is load-bearing, and one is blocked rather than pending.

**Compatibility fixtures.** clauderig builds its own shipped binary at a pinned
commit and diffs the two tools' behaviour over identical inputs, which is how it
notices a change that is invisible to every unit test. A new tool has no baseline
to pin yet — the first release is the baseline — but the harness is worth
standing up then rather than retrofitting, and this is the entry most likely to
be quietly skipped.

**History squash.** No `config-history` side branch, no size-triggered fold. It
matters much less than it did: the growth squashing existed to fix came from
re-committing whole transcripts, and chunking removes that. `repo status` says
when the footprint is lopsided, and `repo gc` is almost always the whole answer.

**`account watch`.** Polling for identity changes. clauderig needs it because
Claude Code's two identity stores drift apart; Codex keeps one, so there is far
less to watch and `account doctor` covers it.

**Desktop profiles.** Codex's desktop host is the ChatGPT app, and no isolated
account-profile launch has been established for it. Blocked on a mechanism to
build against, not on effort — and the first thing to check is whether one
exists, not to invent one.

Everything else in the table is either built or marked ➖ with the reason it does
not apply.
