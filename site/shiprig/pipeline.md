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

::: tip Implementation
The pipeline lives in `shiprig/internal/pipeline` + `forge`; see the
[feature-parity audit](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/FEATURE-PARITY.md)
for the delivered surface.
:::
