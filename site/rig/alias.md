# Aliases

`rig alias` installs short shell aliases for the verbs you reach for most, so
`rig build` becomes `rb` and `rig test` becomes `rt`:

| Alias | Runs | | Alias | Runs |
|-------|------|-|-------|------|
| `rr`  | `rig run`     | | `ri`  | `rig install` |
| `rb`  | `rig build`   | | `rup` | `rig upgrade` |
| `rt`  | `rig test`    | | `rrm` | `rig uninstall` |
| `rf`  | `rig format`  | | `rk`  | `rig kill` |
| `rl`  | `rig lint`    | | `rw`  | `rig watch` (e.g. `rw r`) |
| `rcd` | `rig cd`      | | | |

They're **opt-in** — an alias claims a name in your shell's global namespace, so
rig only adds them when you ask. The set is fixed and deliberately small, and the
names are chosen to avoid shadowing common commands.

## Get them in one command {#setup}

The easiest path is the flag on [`rig setup`](./verbs) — it installs the shell
integration (the `cd` wrapper + tab completion) **and** the aliases together:

```sh
rig setup --aliases        # completion, cd wrapper, and rr/ri/rup/rrm
```

A plain `rig setup` prints a one-line tip pointing at the flag, so you don't have
to remember it.

## The `alias` command {#command}

Prefer to manage the aliases on their own? Use `rig alias`:

```sh
rig alias install          # add the block to your shell startup file
rig alias remove           # take it back out
rig alias list             # show the set, ✓ marking what's live (also: bare `rig alias`)
rig alias install --print  # inspect the snippet without writing anything
```

`rig alias list` reads your startup file and marks the aliases that are actually
installed, so it answers "why doesn't `rt` work" and not just "what could I
install":

```
  ✓ rr   rig run  — Run the project
  ✓ rb   rig build  — Build the project
    rt   rig test  — Run the tests
    …
  ✓ = installed in ~/.zshrc; `rig alias install` adds the rest
```

When rig can't read your startup file (a shell it writes no snippet for) the
legend says so rather than showing everything unmarked, which would read as
"none installed".

The shell is taken from the argument, else `$SHELL`. Supported shells: `zsh`,
`bash`, `fish`, `powershell` (alias: `pwsh`).

```sh
rig alias install fish     # target a specific shell
```

## Choose which ones {#choose}

You don't have to take the whole set. In a terminal, `rig alias install` shows a
checklist (everything pre-checked) so you keep just the aliases you want:

```
Which aliases? (space toggles · enter confirms · esc cancels)
  ✓ rr   rig run
  ✓ rb   rig build
  ✓ rt   rig test
    …
```

For scripts and dotfiles, skip the prompt with `--only` or `--all`:

```sh
rig alias install --only rb,rt,rcd   # just these three
rig alias install --all              # the whole set, no prompt
```

Off a terminal (a pipe, CI, or `rig setup --aliases`), install takes the full set
by default, so non-interactive use is unaffected. The block always renders in the
same canonical order regardless of how you selected, so it stays diff-stable.

**Re-running is an edit, not an append.** The checklist comes up pre-checked with
whatever you already have installed, so unchecking an alias and confirming
*removes* it, and checking a new one adds it — the whole block is replaced with
your current choices. Uncheck everything and confirm to remove the block outright
(same as `rig alias remove`).

## How it works {#internals}

The aliases are written to your shell startup file (`~/.zshrc`, `~/.bashrc`,
`~/.config/fish/config.fish`, or the PowerShell `$PROFILE`) inside their own
marked block:

```sh
# >>> rig aliases >>>
alias rr='rig run'
alias rb='rig build'
alias rt='rig test'
# … rf, rl, ri, rup, rrm, rk, rw …
rcd() { … }
# <<< rig aliases <<<
```

That block is **separate from `rig setup`'s** `# >>> rig shell integration >>>`
block, so the two are managed independently: re-running `rig setup` *without*
`--aliases` never touches your aliases, and `rig alias remove` never touches your
completion or `cd` wrapper. Both splice idempotently — a re-run replaces the
block in place rather than stacking duplicates, so it's always safe to run again.

Each shell gets native syntax. POSIX shells and fish use their `alias` builtins;
PowerShell's `Set-Alias` can't carry arguments, so those aliases are thin
forwarding functions:

```powershell
function rr { rig run @args }
```

### `rcd` is a real function, not a plain alias {#rcd-func}

`rig cd` can only *print* a directory — a subprocess can't change its parent
shell's working directory. The [`cd` wrapper](./verbs) from `rig setup` is what
turns that printed path into an actual `cd`. So `rcd` is rendered as a small
self-contained function that captures `rig cd`'s output and cds for you:

```sh
rcd() {
  local __rig_dir
  __rig_dir="$(command rig cd "$@")" && [ -n "$__rig_dir" ] && builtin cd -- "$__rig_dir"
}
```

Because it calls the binary directly (`command rig`), `rcd` works whether or not
you've also installed the `rig setup` wrapper.

## Why `rrm`, not `run`? {#rrm}

The obvious short name for `rig uninstall` would be `run` — but that would shadow
the ubiquitous `run` command (and the word itself), so an alias meant to run your
project could silently uninstall packages instead. `rrm` reads as "rig remove"
and pairs with `ri` for install.

See [Verbs](./verbs) for the full command surface, and
[`rig setup`](./verbs) for the rest of the shell integration.
