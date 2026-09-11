# codexRig — v2 preview

`codexrig` is the separate Codex tool in rigsmith. Its first preview offers
read-only discovery of configuration and customization candidates. Sync, restore,
queued hooks and release installers are still being built.

Build it from the `codex/v2` source checkout:

```sh
go build -o /tmp/codexrig ./cmd/codexrig
/tmp/codexrig inspect
/tmp/codexrig inspect --json
```

On Windows, choose an output path ending in `codexrig.exe`.

## Inspect

`inspect` checks `CODEX_HOME` (or `~/.codex`) and the shared user-skills directory
`~/.agents/skills`. Use `--codex-home` and `--skills-dir` to supply absolute paths.
Missing sources are reported without creating them. Linked roots are refused;
linked files and directories are not candidates.

The report lists config/profile TOML, instructions, hook definitions, rules and
skill files, together with the processing each still needs before backup. It
reads names and metadata only. Candidate files may contain secrets; this command
is not a security scan or an export.

Native sessions, app databases, known credential files, caches, downloaded plugin
runtimes and automations are excluded. Project/configured extra roots are not
scanned. This preview does not change Codex or ClaudeRig settings or install hooks.
