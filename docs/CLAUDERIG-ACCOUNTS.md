# clauderig accounts — multiple Claude Code logins, one machine

Run several Claude Code accounts (work, personal, a client's) from one machine —
either fully isolated per-terminal, or by swapping the machine-wide login.

> **Credit.** The concept and the key file-fallback session trick come from
> [**claude-swap** by realiti4](https://github.com/realiti4/claude-swap) (MIT).
> `clauderig account` is a clean-room Go reimplementation living inside clauderig;
> all credit for the idea goes to that project.

## Commands

| Command | What it does |
| --- | --- |
| `clauderig account add [--label work]` | Capture the currently logged-in account into rig's store. |
| `clauderig account list` | Show stored accounts; marks the live one with `→`. |
| `clauderig account run <id\|label> [-- claude args…]` | **Session mode** — run Claude Code as that account in *this terminal only*. |
| `clauderig account switch [<id\|label>]` | **Global swap** — change the machine-wide login (all terminals follow). No arg rotates to the next account. |

`acct` is an alias for `account`.

### Interactive screen

Bare `clauderig account` on a terminal (or **Accounts** / hotkey `a` from the
`clauderig` dashboard) opens an interactive screen: it lists tracked accounts
with `→` on the live one, and from there `enter` runs the selected account as a
session, `s` swaps the machine-wide login to it, `a` captures the current login
(prompting for an optional label), and `q` backs out. Like the MCP screen, the
work runs after the screen exits, so `run` cleanly hands the terminal to claude.

## Two mechanisms, deliberately separate

### Session mode (`run`) — the safe one

Claude Code reads `$CLAUDE_CONFIG_DIR/.credentials.json` in preference to the OS
keychain. `account run` exploits that: it writes the chosen account's credential
into a private profile dir (`~/.clauderig/sessions/<id>`) and execs `claude` with
`CLAUDE_CONFIG_DIR` pointed there. Result:

- **Only this terminal** changes account. Every other terminal and the VS Code
  extension stay on your default — so two accounts work in parallel.
- It **never touches the live keychain/credential**, so it can't clobber a
  working login. This composes cleanly with one-worktree-per-chat discipline.

Your `~/.claude` customizations (`settings.json`, `CLAUDE.md`, `keybindings.json`,
`skills`, `commands`, `agents`, `output-styles`, `plugins`) are shared into the
session by default (via symlink, falling back to a copy where symlinks aren't
permitted). Credentials, history (`projects/`, `sessions/`), and global state
(`.claude.json`) stay isolated. Use `--no-share` for a bare profile.

```sh
clauderig account run work                 # interactive, as "work"
clauderig account run personal -- -p "…"   # one-shot, args after -- go to claude
clauderig account run work --no-share      # bare profile, no shared customizations
```

### Global swap (`switch`) — machine-wide

`switch` overwrites the live credential the whole machine reads (macOS Keychain
`Claude Code-credentials`, or `~/.claude/.credentials.json` elsewhere). **Every**
running and future Claude Code instance follows it, so restart any open sessions
after switching. The displaced credential is always backed up first under
`~/.clauderig/cred-backups/` so a bad swap is recoverable by switching back.

```sh
clauderig account switch work   # everything becomes "work"
clauderig account switch        # rotate to the next account
```

Prefer `run` when you want accounts side by side; reach for `switch` only when you
genuinely want the default login to change.

## Storage & safety

- At rest, rig keeps its own copy of each account under
  `~/.clauderig/accounts/<id>/` (`credentials.json` + `meta.json`, mode `0600`).
  The account `id` is a short fingerprint of the OAuth refresh token, so the same
  account always maps to the same id.
- The credential blob is never printed or logged.
- macOS uses the Keychain for the *live* store; Linux/WSL/Windows use the
  file-based `.credentials.json`. Session mode is file-based on every OS.

## Platform notes

- **macOS** — verified against Claude Code 2.x; live store is the Keychain entry
  `Claude Code-credentials`.
- **Windows** — symlink-based sharing needs Developer Mode or an elevated shell;
  otherwise rig falls back to copying customizations into the session.
