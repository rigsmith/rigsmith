# Codex restore safety

Rigsmith validates the files it copies, not Codex's runtime configuration. The
unfinished pinned-schema and vendor-semantic validators have been removed. There
is one preparation path: `adapter.PrepareConfigRestore`.

## Checks that always run

- Bound file counts, sizes and nesting; parse incoming and destination TOML.
- Accept only selected base/profile names; refuse traversal, case aliases,
  links/special files and unsafe backup content.
- Preserve destination-local credentials, paths and protected settings. Retain
  files omitted by the backup; a missing entry is not deletion intent.
- Keep proposed bytes private; previews expose relative names and actions.
- Pin the destination and refuse observed changes before/during application.
- Stage before replacement, coordinate participating writers, and report confirmed
  and uncertain results. Codex and other editors must be idle during writes.

The [codec/path policy](CODEXRIG-V2-CONFIG-PATHS.md),
[preparation](CODEXRIG-V2-CONFIG-RESTORE.md) and
[application](CODEXRIG-V2-CONFIG-APPLY.md) contracts define these guarantees and
limits. Interrupted-restore recovery remains required before a public write command.

## Minimum supported Codex version

The v2 config workflow targets **Codex CLI 0.154.0 or newer**, the latest stable
release checked on September 11, 2026 ([official release](https://github.com/openai/codex/releases/tag/rust-v0.154.0)).
This is a fixed minimum, not an exact-version pin or a moving `latest` requirement.
Older releases are outside the support scope; newer stable releases are not
rejected simply for being newer. The minimum does not certify future file layouts
or runtime behavior.

When the public config workflow is wired, check `codex --version` once at its
command boundary and give an upgrade message for an older release. Keep the
codec and file-safety APIs independent of a Codex executable. The current
inventory-only `inspect` command does not run Codex or enforce this planned check.
No bundled schema or per-version compatibility implementation is required.

## Runtime semantics belong to Codex

Rigsmith does not compile permission policies or globs, resolve managed config
layers, validate provider/MCP/network behavior, check credentials, run configured
helpers or certify startup readiness. There is no exact Codex CLI version gate or
vendored release schema. A syntactically valid configuration may still be refused
by Codex; user-facing results must describe file restoration, not runtime success.
Supported file layouts and isolated smoke-test results will be documented when
the workflow ships. New layouts require adapter work, not a copy of vendor internals.

`ConfigRestoreValidator` is an optional additional integration check of detached
private proposed files. Nil runs the built-in checks above. A supplied callback
can refuse preparation; its private error text does not escape. The callback must
not publish private input or execute configured helpers. It is not a required
implementation of Codex startup semantics. The removed versioned/layered wrappers
have no public command consumers.
