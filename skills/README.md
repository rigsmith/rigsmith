# rigsmith skills

Claude Code [skills](https://docs.claude.com/en/docs/claude-code/skills) that teach
an agent to use the rigsmith CLI family. These live here (not under `.claude/`) so
they can be distributed over GitHub or the website and installed into any machine's
global skill set.

## Skills

| Skill | What it teaches |
|-------|-----------------|
| [`rigsmith-tools`](rigsmith-tools/SKILL.md) | When and how to use `rig` / `changerig` / `shiprig` / `clauderig`, and why to prefer them over raw `go`/`dotnet`/`npm`/`cargo`. |

## Install

Copy a skill into your global skills directory so it's available in every repo:

```sh
# from a rigsmith checkout
mkdir -p ~/.claude/skills
cp -R skills/rigsmith-tools ~/.claude/skills/

# or fetch it straight from GitHub
curl -fsSL https://raw.githubusercontent.com/rigsmith/rigsmith/main/skills/rigsmith-tools/SKILL.md \
  -o ~/.claude/skills/rigsmith-tools/SKILL.md   # mkdir -p the dir first
```

`clauderig sync` then carries `~/.claude/skills` to your other machines.
