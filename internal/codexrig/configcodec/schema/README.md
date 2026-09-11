# Pinned Codex config schema

`codex-0.144.6.json` is an unmodified copy of `codex-rs/core/config.schema.json`
from OpenAI Codex release `rust-v0.144.6`:

- Commit: `5d1fbf26c43abc65a203928b2e31561cb039e06d`
- Source: https://github.com/openai/codex/blob/5d1fbf26c43abc65a203928b2e31561cb039e06d/codex-rs/core/config.schema.json
- SHA-256: `841e0ab1c1bd2fea736ba2d46212ab5bedc06dce9fd83bbafbf50b57b9056d17`
- Retrieved: 2026-09-11
- License and upstream notice: accompanying `LICENSE` and `NOTICE`.

The public schema endpoint changes independently of releases. Updates must pin
and audit a specific release, update the hash/version tests, and review numeric
formats and runtime constraints. Do not refresh this file during a restore.
All references in this snapshot are local; the compiler has no URL/file loaders.
Git attributes preserve the JSON bytes on Windows rather than converting LF to
CRLF, so the embedded schema has the same provenance hash on every platform.

The schema is intentionally not rewritten to remove legacy fields or to add
runtime semantics. Separate code rejects legacy profile selectors/tables and
checks selected provider references. Schema success does not prove runtime
readiness: some objects, including permissions, have permissive schema shapes,
and model-dependent values such as reasoning effort need destination checks.

The built-in provider IDs come from [`built_in_model_providers` in the same
release](https://github.com/openai/codex/blob/5d1fbf26c43abc65a203928b2e31561cb039e06d/codex-rs/model-provider-info/src/lib.rs#L430).
The adjacent `merge_configured_model_providers` keeps existing built-ins for
ordinary colliding keys; it permits Bedrock `aws.profile`/`aws.region` settings
and rejects other non-default Bedrock fields. Blanket collision refusal would
reject valid native settings. This structural layer checks provider references;
the mandatory destination validator must enforce the runtime merge constraints.
