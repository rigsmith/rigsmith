# codexrig

Sync your Codex CLI configuration across machines, and run several Codex logins
side by side on one of them.

codexRig is claudeRig's sibling, not its generalisation. The two hard problems
are the same — correcting paths across operating systems, and never leaking a
secret — but Codex's layered TOML configuration, its isolated `CODEX_HOME`
accounts and its date-sharded session rollouts are its own, and the places where
Codex differs from Claude Code are the places where a direct port would quietly
break.

## What it does

```sh
codexrig init            # a private remote, and the hooks that keep it current
codexrig global trust    # let Codex actually run those hooks
codexrig sync            # capture and push
codexrig restore         # on another machine
```

Full docs: <https://rigsmith.dev/codexrig/>

## The shape of it

| Package | What lives there |
|---|---|
| `internal/codexrig/codexhome` | The vendor seam: everywhere Codex keeps state. One file to edit when a Codex release moves something. |
| `internal/codexrig/allowlist` | Policy — which files may leave the machine. Default-deny. |
| `internal/codexrig/codec` | The JSON and TOML codecs the engine dispatches on. |
| `internal/codexrig/engine` | Capture and restore. |
| `internal/codexrig/rollout` | Reading Codex's session files, always bounded. |
| `internal/codexrig/account` | Several logins, each in its own `CODEX_HOME`. |
| `internal/codexrig/hooks` | Installing into Codex's lifecycle, and getting trusted. |
| `internal/codexrig/appserver` | A small JSON-RPC client, for the few things Codex should be asked. |
| `internal/codexrig/guard` | The PreToolUse hook. |
| `internal/agentrig/*` | Shared with clauderig: the secret scanner, the allowlist matcher, the private-remote gate. |

## Five things worth knowing before changing any of it

Each of these was established by asking Codex or by a test failing, not by
reading documentation, and each is the kind of thing that fails silently.

**Codex's hooks file uses PascalCase event keys.** Not camelCase, which is the
app-server's wire form, and not snake_case, which is what Codex's own trust keys
use. Both of those parse and are then ignored, because the parser does not reject
unknown fields — so a hook under the wrong key produces no error, no warning, and
no hook.

**An installed hook does not run until it is trusted.** Codex records a hash in
`config.toml` and declines anything else without a word. That makes
installed-but-untrusted the quietest way for a backup tool to stop backing up,
which is why `doctor` asks Codex directly rather than checking that a file exists.

**Codex writes absolute paths as TABLE KEYS**, not only as values:
`[projects."/Users/someone/Git/thing"]`. Claude Code does not, so a path rewriter
ported from clauderig walks only values and leaves every one of those spelled for
the machine it came from.

**Rollout bytes are never edited.** Claude Code's project directories are *names*
derived from a path, so clauderig can translate them. Codex records the working
directory inside the rollout, so the equivalent would mean rewriting the middle of
a conversation in order to back it up. `codex resume --cd` exists for the case
where a directory moved.

**`auth.json` is the whole secret.** There is no OS credential store behind it in
0.144.6 — `codex features list` reports `secret_auth_storage` as false — so a copy
of that file is a working login. It is excluded from the allowlist, excluded
again by name in the tests, and never copied anywhere but an account's own
directory.

## Tests

```sh
go test ./internal/codexrig/... ./internal/agentrig/...
```

Two suites are gated, because they need things a CI runner does not have:

```sh
CODEXRIG_LIVE_CODEX=1 go test ./internal/codexrig/hooks/    # needs the codex binary
CODEXRIG_REAL_DATA=1  go test ./internal/codexrig/rollout/  # reads this machine's own sessions
```

The live hook test is the one that matters most: it installs hooks into a
temporary `CODEX_HOME`, asks a real `codex app-server` whether it can see them,
trusts them, and asks again. Nothing but a running Codex can notice when one of
the facts above changes.
