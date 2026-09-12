# codexRig

Sync your Codex CLI configuration across machines, and run several Codex logins
side by side on one of them.

```sh
curl -fsSL rigsmith.sh/install/codexrig | sh
```

codexRig is claudeRig's sibling: same idea, a different agent. It copies your
Codex setup into a private git repo of your own, rewrites machine-specific paths
so it lands correctly on a computer laid out differently, and refuses to publish
anything that looks like a credential.

## What travels, and what does not

| Carried | Never carried |
|---|---|
| `config.toml` and named profile overlays | `auth.json` — your login |
| `AGENTS.md` | the SQLite databases and their write-ahead logs |
| `skills/`, `prompts/`, `rules/`, `themes/` | `plugins/`, `cache/`, `log/`, `computer-use/` |
| session rollouts, when you ask for them | `installation_id`, hook trust hashes, shell snapshots |

Nothing syncs unless a rule allows it. That is the safety property: a Codex
release that starts writing a new secret-bearing file is excluded until somebody
says otherwise, rather than being published on the next run because nobody
thought to exclude it.

**Your credential is never in the backup.** A restored machine runs `codex login`
once for itself. Values that look like tokens — anything under an
`[mcp_servers.*.env]` table, for instance — are replaced with a sentinel, and a
restore puts the machine's own value back rather than writing the sentinel over
it.

## Sessions are opt-in

Your conversations are not backed up until you ask:

```sh
codexrig config set syncSessions true
```

They are large, and cross-machine resume is not a proven round trip yet — so
carrying them is a backup rather than portability, and that is a choice worth
making deliberately. A rollout is never rewritten: it is a conversation, not a
config file, so it travels byte for byte.

Large ones are stored as content-addressed parts, which matters more than it
sounds. The biggest rollout on the machine this was built against is 172 MB — over
the default per-file cap, so without that it is the one conversation never backed
up at all. Chunked, it becomes 44 parts and a 4 KB index, and one more turn
rewrites one of them.

## Getting set up

```sh
codexrig init            # pick a private remote, install the hooks
codexrig global trust    # let Codex actually run them
codexrig sync            # capture and push
```

That second step matters. Codex will not run a hook until its hash is recorded,
and it says nothing at all when it declines — so an installed-but-untrusted hook
is the quietest possible way for a backup tool to stop backing anything up.
`codexrig doctor` is the command that notices.

On a second machine:

```sh
codexrig init --remote <same private repo>
codexrig restore
codex login
```

## Several logins on one machine

```sh
codexrig account add                 # track the login you are signed in as
codexrig account run work            # start Codex as that login
codexrig account switch personal     # change which login plain `codex` uses
```

Each account gets its own `CODEX_HOME`, so two can run at once, with your setup
— `config.toml`, `AGENTS.md`, skills, rules — shared in so they differ only in
who they are signed in as. `switch` refuses while Codex is running: a live
session holds the credential it started with, and swapping underneath it leaves
it unable to refresh.

## Is it working?

```sh
codexrig status
codexrig doctor
```

Every way a backup tool fails is quiet. A rejected push leaves a machine looking
synced; a remote that stopped being private looks exactly like one that never
was. `doctor` is where those become sentences, and it exits non-zero while
anything is still wrong.
