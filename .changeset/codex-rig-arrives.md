---
type: feat
scope: codexrig
"github.com/rigsmith/rigsmith"
---

codexRig: sync your Codex CLI configuration across machines, and run several Codex logins side by side.

The fifth rig does for the Codex CLI what claudeRig does for Claude Code. It copies your setup — `config.toml` and any named profile overlays, `AGENTS.md`, skills, prompts, rules — into a private git repo of your own, rewrites machine-specific paths so it lands correctly on a computer laid out differently, and refuses to publish anything that still looks like a credential.

```sh
codexrig init            # a private remote, and the hooks that keep it current
codexrig global trust    # let Codex actually run them
codexrig sync
```

Your login is never in the backup. `auth.json` is excluded, and a value that looks like a token — anything under an `[mcp_servers.*.env]` table, for instance — is replaced with a sentinel that a restore swaps back for the machine's own value. A restored machine runs `codex login` once for itself.

**Your conversations are not backed up unless you ask.** `codexrig config set syncSessions true` turns rollouts on. They are large, and resuming one on another machine is not a proven round trip yet, so carrying them is a backup rather than portability — and every surface says which mode the machine is in, so nobody assumes otherwise.

**Several logins on one machine.** `codexrig account add` tracks the login you are signed in as; `codexrig account run work` starts Codex as that one without disturbing the others, each in its own `CODEX_HOME`, with your setup shared in so they differ only in who they are. `codexrig account switch` changes which login a plain `codex` uses, and refuses while Codex is running — a live session holds the credential it started with, and swapping underneath it leaves it unable to refresh.

**One thing to know about the hooks.** Codex will not run a hook until its hash is recorded in `config.toml`, and it says nothing at all when it declines. That makes installed-but-untrusted the quietest possible way for a backup tool to stop backing anything up, which is why `install` tells you to run `trust` every time and `codexrig doctor` asks Codex directly rather than checking that a file exists.

Three things differ from claudeRig because Codex differs, and each would have failed quietly if ported straight across. Codex writes absolute paths as TOML table *keys* (`[projects."/Users/you/Git/thing"]`), so a rewriter that walks only values leaves a restored config trusting a directory that does not exist. Codex's configuration is TOML, so it goes through a real codec rather than a second suffix test — a raw-file path would carry `config.toml` past field-level redaction entirely. And a rollout records its working directory *inside* the conversation, so codexRig never rewrites one: `codex resume --cd` is the answer when a directory moved.

`codexrig doctor` is where the quiet failures become sentences, and `docs/CODEXRIG-PARITY.md` lists, feature by feature, what is built and what is not.
