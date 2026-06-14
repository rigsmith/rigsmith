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

::: tip Design reference
See the [release orchestrator design](https://github.com/JohnCampionJr/rigsmith/blob/main/docs/RELEASE-ORCHESTRATOR.md)
for the full mapping from net-changesets and the `shiprig/internal/pipeline` +
`forge` implementation.
:::
