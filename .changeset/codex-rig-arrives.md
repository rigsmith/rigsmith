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

**Large conversations.** A rollout past 8 MiB is stored in the repo as content-addressed parts rather than as one blob, so adding a turn costs a chunk instead of a copy. This matters more than it sounds: the biggest rollout on the machine this was built against is 172 MB, which is over the default per-file cap — so without it, the longest conversation you have is the one thing never backed up. Chunked, it becomes 44 parts and a 4 KB index, and one more turn rewrites one of them.

`codexrig peek` reads another machine's session straight out of the repo without restoring anything, and `peek get` copies just that one session onto this machine. `codexrig ledger` remembers a session after its body ages out of the retention window, so a search for an old conversation says "this existed, and here is the command that recovers it" rather than nothing. `codexrig repo status` says what the backup holds by category, because a byte total on its own points at the wrong lever.

`codexrig account map <account>` binds a directory, so a bare `account run` anywhere under it picks that login.

`codexrig doctor` is where the quiet failures become sentences, and `docs/CODEXRIG-PARITY.md` lists, feature by feature, what is built and what is not.
