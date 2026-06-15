# The release pipeline

`shiprig release` runs a configurable step pipeline defined in
`.changeset/release.jsonc` — the orchestration layer on top of `version`,
`tag`, and `publish`. It's the Go port of net-changesets' release orchestrator.

```sh
shiprig release
```

## `.changeset/release.jsonc`

The pipeline file describes:

- **steps** — the ordered units of work the release runs through;
- **hooks** — commands to run around steps;
- **vars** — values interpolated into steps;
- **confirm gates** — interactive confirmation points (bypass with `--yes` in CI);
- **secret masking** — values scrubbed from logs;
- **forge releases** — GitHub releases created as part of the run.

Because each step is explicit, the same pipeline runs the same way locally and
in CI — the only difference is the confirm gates, which `--yes` skips.

## Environment & `.env`

Before running, `shiprig release` loads `.env` and `.env.local` from the repo
root and layers them **under** the ambient shell environment (`.env` <
`.env.local` < exported variables — a real `export` always wins). That merged
environment is what every part of the run sees:

- `${env.NAME}` placeholders in steps, hooks, and vars resolve from it;
- the commands each step runs (publish, tag, push) inherit it;
- forge (GitHub) releases run with it, so `gh` finds its token;
- `shiprig init`'s token preflight checks it, so a token kept in a local `.env`
  reads as ✓ set rather than a false ⚠.

This means a release token can live in `.env.local` (git-ignored) instead of
being exported in every shell. Values are still scrubbed from logs by secret
masking, and `.env` files are read, never written or printed.

Pass `--no-env` to drop the `.env`/`.env.local` layer for a run (the ambient
shell environment still flows through) — handy when a stray local `.env` would
otherwise shadow what you've exported.

::: tip Implementation
The pipeline lives in `shiprig/internal/pipeline` + `forge`; see the
[feature-parity audit](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/FEATURE-PARITY.md)
for the delivered surface.
:::
