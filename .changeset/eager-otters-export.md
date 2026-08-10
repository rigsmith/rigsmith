---
type: fix
---

`shiprig publish` now loads `.env`/`.env.local`, so a registry key kept in a local `.env` reaches the push instead of the push going out with no credential.

Only `release` and `init` ever called the env loader. A direct `shiprig publish` ran with the bare ambient environment, so the dotnet adapter's `NUGET_API_KEY` fallback (and core/auth's `env:NAME` references, and the OIDC context probe — all `os.Getenv` lookups) saw nothing: `dotnet nuget push` ran without `--api-key` and the feed rejected it. Exporting the same variable into the shell fixed it, which is what made this env loading rather than the key. The persistent `--no-env` flag was documented on `shiprig publish --help` as "skip .env/.env.local loading for this run" while that command never loaded any.

`publish` now layers `.env`/`.env.local` under the ambient environment (a real `export` still wins) and *exports* the result for the run, before anything resolves a credential. Exporting rather than threading the map through `plugin.PublishRequest` is what the publish path needs: every credential lookup on it reads the process environment, and the adapters spawn their package manager with an inherited one — which is exactly why the same publish works under `shiprig release`, where it runs as a subprocess seeded with that environment. `--no-env` skips the file layer, making it a no-op.

Auditing the other holders of the persistent flag turned up two more:

- `shiprig doctor` probed `gh auth status` with the ambient environment only, so a `GH_TOKEN` declared in `.env` reported "not authenticated" while the release that check gates authenticated fine. It now probes with the layered view, honouring `--no-env`. It also *reports* an unreadable `.env` as a failure rather than quietly falling back to the ambient environment: `release` and `publish` both refuse to start on that error, so the one command whose job is to warn you should not be the one hiding it. The `gh` probe still runs, so a single bad file doesn't blank the rest of the report.
- `rig`'s custom commands (`.rig.json` `commands`) loaded `.env` *regardless* of `--no-env` — the same flag inert in the opposite direction. Both env builders did it: `customEnv` for the shell/argv forms, and `customEnvMap` for the script form, where it feeds a Tengo script's `ctx.env` *and* the runner its `sh()` calls go through. They now share one file-layer reader that honours the flag, matching the built-in verbs.

`shiprig tag` takes the flag too but creates local tags only — no credential, nothing for the layer to feed. The remaining subcommands (`add`, `status`, `version`, `info`, `config`, `packages`, `pre`, `ui`) are inherited from changerig and do no env-dependent work.

Four tests cover it: an end-to-end `publish` run against a fake `dotnet` on PATH asserts the push carries `--api-key` from a key present only in `.env` (verified to fail on the old behaviour, with the reported keyless push), a `--no-env` run asserts it does not, and unit tests pin the export precedence and the `rig` custom-command flag across every command form.
