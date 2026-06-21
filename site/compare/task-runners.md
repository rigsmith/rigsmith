# rig vs make / just

`make`, [`just`](https://github.com/casey/just), and similar task runners are
**recipe-first**: you write the commands, they run them. `rig` is
**convention-first**: it already knows what `build`, `test`, `run`, and `format`
mean in .NET, Node, Go, and Rust, so there's nothing to write to get started.

| | make / just | rig |
|---|---|---|
| Model | You define every recipe | Built-in verbs, detected per ecosystem |
| Zero-config start | No — empty repo does nothing | Yes — `rig build` works immediately |
| Cross-ecosystem | You write per-project recipes | Same verb, right native command everywhere |
| Discovery | — | `rig info` shows what it found; Node scripts become verbs |
| Custom commands | The whole point | `commands` in `.rig.json` (shell / argv / cross-platform Tengo script) |
| Coverage / kill / doctor | Hand-rolled | First-class verbs |

## Not actually rivals

The honest framing: `rig` and `just` solve overlapping but different problems.
`just` is a great *command memory* — a place to park the project-specific
incantations that have no convention. `rig` handles the conventional dev loop
(`build`/`test`/`run`/`format`/`coverage`) so you *don't* write recipes for the
things every project already does the same way.

In practice they compose: let `rig` own the standard verbs, and put genuinely
bespoke tasks in `.rig.json`'s `commands` (which run cross-platform by default —
portable shell or a Tengo `script` — with `env` and `cwd`) — or keep a `justfile`
alongside if you prefer. `rig` doesn't
try to be your whole automation layer; it tries to make the 90% case need no
configuration at all.
