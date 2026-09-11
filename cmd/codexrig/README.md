# codexrig (v2 preview)

The separate Codex frontend for rigsmith. This first preview inventories possible
configuration and customization inputs; it does not sync, restore or run a queue.

Build from the `codex/v2` source checkout:

```sh
go build -o /tmp/codexrig ./cmd/codexrig
/tmp/codexrig inspect
/tmp/codexrig inspect --json
/tmp/codexrig inspect --codex-home /absolute/codex-home --skills-dir /absolute/user-skills
```

On Windows, use an appropriate output path ending in `codexrig.exe`.
Release installers do not include this preview yet.

`inspect` uses `CODEX_HOME`, falling back to `~/.codex`, plus the separate shared
user-skills root `~/.agents/skills`. Flags override their respective roots and
require absolute directories. Changing `CODEX_HOME` does not relocate shared
skills. Missing roots are reported without creating them. Root symlinks are
refused; linked files and directories are not inventory candidates.

Output contains paths and classifications, never file contents. Each candidate
names the capture policy it still needs. Candidate status is not a secret scan
or permission to copy: config TOML, hooks JSON, text and skill assets require
separate processing before publication. Native sessions, history, databases,
automations, memories, plugin caches and known credential files are excluded.
Project/configured customization roots are not discovered in this preview.

See the [adapter contract](../../docs/CODEXRIG-V2-FOUNDATION.md) and
[roadmap](../../docs/CLAUDERIG-SHARED-LAYERS-ROADMAP.md). ClaudeRig's command tree,
installed hooks and backup formats are independent of this executable.
