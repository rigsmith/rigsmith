# codexRig — design

codexRig syncs a Codex CLI setup across machines through a private git repo of
the user's own, and runs several Codex logins side by side on one machine.

It is claudeRig's sibling, not its generalisation. The two hard problems are the
same — correcting paths across operating systems, and never leaking a secret —
and the rest is different in ways that matter. This document records the
decisions, and in particular the ones where a direct port from clauderig would
have been wrong.

Companion: [CODEXRIG-ASSESSMENT.md](CODEXRIG-ASSESSMENT.md), written before any
of this existed. Most of its warnings were correct; where the implementation
departs from it, this document says why.

## What Codex actually keeps, and where

Established against `codex-cli 0.144.6`, a 1.0 GB `~/.codex` on a real machine.

| Path | What it is | Synced |
|---|---|---|
| `config.toml` | The user config: model, features, MCP servers, plugins, project trust | yes, through the TOML codec |
| `<name>.config.toml` | A named profile overlay (`codex --profile <name>`) | yes |
| `AGENTS.md` | Global instructions | yes |
| `skills/`, `prompts/`, `rules/`, `themes/` | What you have taught Codex | yes, minus `skills/.system` |
| `sessions/YYYY/MM/DD/rollout-*.jsonl` | Session transcripts | opt-in |
| `archived_sessions/`, `session_index.jsonl` | Archived sessions, the thread-name index | opt-in, with the rollouts |
| `auth.json` | **The login** | never |
| `*.sqlite` + `-wal` + `-shm` | Live databases: threads, history, logs, queue, goals, memories | never |
| `plugins/` (315 MB), `cache/` (25 MB), `computer-use/` (69 MB) | Runtimes and caches | never |
| `installation_id`, `[hooks.state]`, `shell_snapshots/`, `ipc/`, `thread-writer-locks/` | This machine, and only this machine | never |
| `automations/` | Scheduled work | never — restoring it would run every job twice |

`logs_2.sqlite` alone is 85 MB of telemetry. `thread_history_1.sqlite` is a
projection of the rollouts keyed by byte offset into them, so the rollouts are
the artifact worth having and the database is not.

## The decisions

### Rollout bytes are never edited

clauderig rewrites Claude Code's project slugs, because a slug is a directory
*name* derived from a path: translating it moves a file without touching its
contents. Codex records the working directory *inside* the rollout, so the
equivalent would mean rewriting the middle of a conversation in order to back it
up — producing an artifact whose replay nobody can vouch for.

So a rollout travels byte for byte. `codex resume --cd` exists for the case where
a directory moved, and the manifest carries a portable spelling of each working
directory so a listing can still show something meaningful on another machine.

The one exception is opt-in and explicit: `redactTranscripts` scrubs
credential-shaped tokens out of the *staged* copy. The live file is never
touched. It is off by default, because rewriting a conversation is a thing a
backup tool should do only when asked.

### Sessions are opt-in

Configuration is portable: it is small, it means the same thing on every machine,
and restoring it is the point of the tool. A rollout is none of those. It is
large, its cross-machine resume is not a proven round trip, and carrying it is
backup rather than portability.

Treating those two as one setting would have meant either forcing gigabytes on
somebody who wanted their skills back, or quietly not backing up the
conversations of somebody who assumed they were. `codexrig init`, `status` and
`doctor` all say which mode the machine is in, unprompted.

### TOML is a codec, not a second suffix test

clauderig's entire format dispatch is `strings.HasSuffix(rel, ".json")`, because
Claude Code's configuration is all JSON. Codex's is TOML, and the wrong response
is a second suffix test beside the first: a raw-file path for TOML would carry
`config.toml` through verbatim, past field-level redaction and past the
secret-preserving restore. That is how a token in an `[mcp_servers.x.env]` table
reaches a git remote.

So a format is a `Codec`: it decodes to the generic tree the redactor and the
path rewriter already understand, and encodes back deterministically. Everything
between those two calls is format-blind.

The cost is real and is stated rather than hidden: TOML comments do not survive a
decode/encode round trip. In the staged copy that is harmless — it is a derived
file. On restore it would mean overwriting a commented local config with an
uncommented one, so restore compares semantically first and writes nothing when
the merged result already matches what is on disk. A machine whose config did not
change upstream keeps its comments; one that genuinely changed loses them, and
the restore names the files it rewrote.

### Absolute paths appear as table KEYS

This is the difference that a port would most easily miss, because it fails
quietly:

```toml
[projects."/Users/someone/Git/thing"]
trust_level = "trusted"

[hooks.state."/Users/someone/Git/thing/.codex/hooks.json:pre_tool_use:0:0"]
trusted_hash = "sha256:…"

[desktop.open-in-target-preferences.perPath]
"/Users/someone/Git/other" = "vscode"
```

Claude Code does not address settings by path, so clauderig's rewriter walks
values only. Ported unchanged, every one of those keys arrives on the second
machine spelled for the first — and the symptom is that a restored config trusts
a directory that does not exist there, and does not trust the one that does.

Keys are rewritten too. The test for "is this a path" is `Portablize` itself: it
only succeeds for something under a known folder, so an ordinary key like
`features` is left alone by construction rather than by a list of exceptions. A
compound hook key keeps its `:event:group:handler` tail, split from the right so
a Windows drive letter is not mistaken for the delimiter.

Project trust is carried deliberately; hook trust is dropped. A `trust_level`
entry is a portable path plus a decision, and dropping it would end a restore
with the user re-trusting every repository by hand. A `trusted_hash` entry is an
absolute path plus a content hash of a file that may not exist elsewhere —
restoring it grants nothing.

### There is no desync model, because Codex has no second place to disagree

Most of clauderig's account code exists because Claude Code splits identity in
two: a credential in the Keychain and a display block in `~/.claude.json`, which
can name different accounts. Codex keeps one file, and the identity is *inside*
it — `auth.json`'s `id_token` is a JWT whose claims carry the email, the plan and
the account id.

So the only faults possible are: the credential is missing, it cannot be read, it
cannot authenticate, or codexrig's own pointer names somebody else. Those are the
four `Problems()` reports, and there is no fifth. Anyone porting one in from
clauderig is solving a problem Codex does not have.

The JWT is decoded without verifying its signature, which is correct rather than
lazy: codexrig is not deciding whether to trust the token — the server does that
on every request — it is reading the label the user already agreed to, in order
to file the credential under the right name. An expired token still names its
owner.

### Accounts are isolated homes

`CODEX_HOME` relocates everything Codex keeps, which is the counterpart to
`CLAUDE_CONFIG_DIR`. Each tracked account gets `~/.codexrig/accounts/<id>/home`,
with the shared setup — `config.toml`, `AGENTS.md`, `skills`, `rules` — symlinked
back in, so several logins differ only in who they are signed in as. `auth.json`
is never shared; that is the whole reason the homes are separate.

Naming is by email, never by a token: Codex rotates the refresh token on every
refresh, so a token-keyed account would become a different account several times
a day.

`switch` refuses while Codex is running. A live session holds the credential it
started with, and swapping underneath it leaves it unable to refresh. It stores
the displaced credential back under its own account first, so a rotated token
survives switching away and back, and it backs up what it displaced.

A Codex process with no `CODEX_HOME` in its environment is resolved against *its
own* `HOME`, not the caller's. Getting that wrong made every unrelated Codex on a
machine block a swap.

### Hooks: three facts, all of which fail silently

None of these is guessable, and each was established by asking a running Codex.

**The file uses PascalCase event keys.**

```json
{ "hooks": { "SessionStart": [ { "hooks": [ { "type": "command", "command": "codexrig pull" } ] } ] } }
```

Not `sessionStart`, which is the app-server's wire form, and not `session_start`,
which is what Codex's own trust keys use. Both parse and are then ignored,
because the parser does not reject unknown fields. A hook under the wrong key
produces no error, no warning, and no hook.

**`async: true` is parsed and then skipped**, with a warning that nothing reads.

**An installed hook does not run until it is trusted.** Codex records
`[hooks.state."<path>:<event>:<g>:<h>"].trusted_hash` in `config.toml` and
declines anything else without a word. Installed-but-untrusted is therefore the
quietest possible way for a backup tool to stop backing up, which is why `install`
says so every time and `doctor` asks Codex directly rather than checking that a
file exists.

`trust` is a separate command because it is a separate act: one writes a file,
the other tells Codex to trust a file. It asks Codex for the hash rather than
computing one — the hash is Codex's opinion about a file, not ours — and makes
the edit *through* Codex's own `config/value/write`, so `config.toml` keeps
whatever formatting Codex keeps instead of surviving a TOML round trip.

All three facts are pinned by a gated live test that installs hooks into a
temporary `CODEX_HOME`, asks a real `codex app-server` whether it can see them,
trusts them, and asks again. Nothing but a running Codex can notice when one of
them changes.

### The guard is narrower, and judges a patch whole

clauderig also refuses the tools that relocate a session, because Claude Code
keys its chat history to the working directory. Codex has no such tools and
records the directory per thread, so those refusals have no counterpart and
inventing one would be theatre.

What is new is the shape of a change. Claude Code edits one file per call, with
the path in `tool_input.file_path`. Codex applies a patch: `apply_patch` carries a
document that may add, update, delete and move several files at once. A guard
reading one path per call sees the first file — often a README — and waves the
rest through.

The matcher and the switch are built from one list, because in the sibling tool
they were separate and a new command-bearing tool added to one but not the other
silently disabled every rule.

The whole thing fails open: every error path defers, so a bug can only let
something past, never stop somebody working.

### Shared, not forked

Two engines moved to `internal/agentrig` rather than being copied:

- **`redact`** — the secret scanner, the credential rules, the entropy backstop
  and the secret-preserving merge. This is the entire safety claim of both tools,
  and two copies of a safety claim diverge; the copy that falls behind is the one
  quietly publishing a token. `Placeholder` keeps its `__CLAUDERIG_REDACTED__`
  spelling in both, because the sentinel is WIRE FORMAT: it sits in committed
  files that a restore on another machine has to recognise.
- **`allowlist`** — the rule engine. "Which files may travel" is a judgement
  about one CLI's layout; longest-match-wins and default-deny are properties the
  tools must not disagree about.
- **`ghrepo`** — the private-remote gate, whose messages became tool-agnostic
  rather than gaining a parameter. A gate that has to be told who it is working
  for is a gate that can be told the wrong thing.

clauderig's own packages keep their exact surface through aliases, so every call
site there reads as it did.

Deliberately **not** shared: the sync engine. The assessment proposed extracting
it behind adapter interfaces, and that is still the right end state, but doing it
while writing the second vendor would have meant designing the seam from one
example and regression-testing clauderig on every commit. The Codex engine is its
own, the two are close enough to compare, and the extraction is now a refactor
with two real implementations to generalise over rather than one and a guess.

## What is not built

Written down so nobody assumes otherwise. See the parity table in
[CODEXRIG-PARITY.md](CODEXRIG-PARITY.md) for the full accounting.

- **Chunked rollout storage.** clauderig splits a large transcript into
  content-addressed 4 MB parts so an append costs one blob rather than a whole
  copy. codexrig has the large-file throttle and the size cap but not the
  chunking, so a very long session costs more history than it needs to.
- **A permanent session ledger.** clauderig remembers a session after its body
  ages out, so a search can say "this existed, recover it from git history"
  rather than "no such conversation". With rollouts opt-in this matters less, but
  it is a real gap when they are on.
- **Desktop profiles.** Codex's desktop host is the ChatGPT app, and no isolated
  account-profile launch has been established for it. The assessment said to
  treat this as separate work, and it still is.
- **`peek`.** Reading another machine's session straight out of the git object
  store, without restoring.
- **Automations.** Excluded rather than solved: restoring a scheduled job onto a
  second machine would run it twice.
