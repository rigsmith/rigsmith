# CodexRig foundation (8a)

`codexrig inspect` is the first executable Codex adapter slice. It inventories
source names and metadata, with text or versioned JSON output. It does not read
file contents, parse config, invoke Codex, log in, execute hooks, create tool state,
write a backup or claim that a session can be restored. It needs no Codex process
or credentials. All validation fixtures are synthetic.

## Source contract

Sources are independent: `--codex-home` overrides `CODEX_HOME`, whose fallback is
`~/.codex`; `--skills-dir` overrides `~/.agents/skills`. The latter is shared user
customization, not a subdirectory relocated by `CODEX_HOME`. All overrides must
be absolute and nonempty. Missing directories report `present: false`; other
inspection errors stop the command before emitting a report. Roots are never
created. A root must be a directory, not a file or final-component symlink.

The shared `internal/agentrig/allowlist` walker provides matching, pruning and
sorting. Its new optional `WalkContext` checks cancellation between entries;
existing Claude callers retain `Walk` and its behavior. Filesystem syscalls can
still outlast cancellation. Codex then applies exact native path classification
and accepts regular files only. File and directory links are not selected, even
when their targets are inside the source. This is a live inventory, not a sealed
snapshot: later capture must revalidate source identity and content under its own
coordination contract.

| Source / relative path | Candidate kind | Required before capture |
| --- | --- | --- |
| Codex `config.toml` | config | Structured TOML, secret handling and path policy |
| Codex `NAME.config.toml` | config-profile | Same TOML processing; NAME uses ASCII letters, digits, `_` and `-` |
| Codex `AGENTS.md`, `AGENTS.override.md` | instructions | Text scanning and path policy |
| Codex `hooks.json` | hooks | Structured hook processing; no command execution |
| Codex `rules/*.rules` (direct children) | rules | Text scanning and path policy |
| Codex `skills/NAME/...` | legacy skill files | Reviewed skill-content policy |
| User skills `NAME/...` | shared skill files | Reviewed skill-content policy |

These are candidate file shapes, not validation of skill packages or their
frontmatter. There is no raw-copy fallback. Unknown root kinds and file shapes
are rejected. Relative paths containing traversal, backslashes, colons, invalid
UTF-8 or control characters are not candidates. Hidden segments, dependency
caches, known credential filenames, key/certificate files and database filenames
are excluded inside selected skill trees too. Final exclusions are insensitive
to filename case. Unknown files outside the selected trees are excluded by default.

Sessions and archived sessions, `history.jsonl`, session indexes, SQLite/WAL
state, shell snapshots, logs, caches, worktree checkouts, downloaded plugins,
scheduling definitions, and memories are not selected. Configured extra roots,
project-local config and linked skills require later explicit discovery policy.
Credentials may still appear inside a candidate's contents: inventory does not
read, sanitize or approve any content for backup.

## Separate tool, shared mechanics

Dependencies flow from `cmd/codexrig` through Codex commands and adapter to shared
allowlist mechanics. No Codex package imports `internal/clauderig`, and no shared
package imports either vendor. This slice does not create a CodexRig config/state
schema, remote, queue or installer entry. Those will be independent of ClaudeRig;
preview builds are source-only until the release stage.

The `--json` envelope has `version: 1`, `mode: "inventory-only"`, and ordered roots
with `root`, `path`, `present`, and `candidates`. Each candidate has `path`, `kind`
and `requires`; empty candidate arrays are `[]`. Human output quotes paths to
escape terminal control characters. Neither output includes source contents.

## Evidence and next gates

Official documentation verified September 11, 2026:

- [State/config locations and named profiles](https://learn.chatgpt.com/docs/config-file/config-advanced): Codex home defaults, separate named TOML overlays and hook locations. The profile-file layout is documented for Codex 0.134.0 and later.
- [Credential storage](https://learn.chatgpt.com/docs/auth): file-backed `auth.json` and OS credential-store modes remain Codex-owned.
- [Skills](https://learn.chatgpt.com/docs/build-skills) and [instructions](https://learn.chatgpt.com/docs/agent-configuration/agents-md): customization roots and instruction discovery. This inventory covers only the listed user roots.
- [Configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference): SQLite-backed state is runtime data and can have a separately configured location; this command does not open it.

Local installed version was checked as `codex-cli 0.144.6`; no live credentials,
configuration values or transcript contents were inspected. The earlier
[assessment](CODEXRIG-ASSESSMENT.md) records version-specific session observations,
not a public export contract.

The internal [TOML codec](CODEXRIG-V2-CONFIG-CODEC.md) starts config portability
(8b.1); it does not change this command or enable file capture. Remaining config
capture/restore integration (8b) is followed by native
session preservation and discovery/resume validation (8c), Codex queue/hook
integration (8d), and packaging/platform release validation (8e). Until those
land, do not advertise sync, native session recovery or account/Desktop parity.
