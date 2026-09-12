---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

The MCP travel verdict now checks whether `.mcp.json` is actually committed, and stops giving absolute paths under your home a pass.

"It travels with your repo" is a claim about git, so it is now checked against git: a `.mcp.json` that is gitignored or was never added reads `no`, because a clone does not get it, and if git cannot be asked the column reads `unchecked` rather than guessing.

Every absolute path is reported, including one under `$HOME`. Rewriting paths for the next machine is something clauderig does to files it carries, and it does not carry this one — git moves `.mcp.json` byte for byte, so a path under your own home is exactly as broken on a machine with a different home. Arguments are named individually (`args[3]`), and UNC paths (`\\server\share\…`) are recognised as absolute.

Also: `recent --json` always emits `query`, matching `search --json`; and a condemned staged file that cannot be deleted is now reported instead of passing silently, because the refusal on its own reads as "nothing left this machine".
