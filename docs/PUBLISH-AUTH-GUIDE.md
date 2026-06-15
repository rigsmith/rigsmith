# Publish authentication

`shiprig publish` authenticates to each registry in one of three ways, chosen by
precedence. You don't pick a mode globally — you configure a source, and shiprig
uses the best one available at publish time:

1. **OIDC trusted publishing** — tokenless, for CI (GitHub Actions / GitLab).
2. **A secret reference** — `op://…` (1Password), `env:NAME`, or `cmd:…`, for
   local/manual publishing or any non-OIDC context.
3. **Ambient credentials** — whatever the package manager already has
   (`~/.npmrc`, `cargo login`, `NUGET_API_KEY`, stored feed creds). This is the
   pre-configuration default, so existing setups keep working untouched.

Precedence per registry: an explicit secret ref wins; otherwise OIDC when a CI
OIDC context is present and not turned off; otherwise ambient.

Resolved secrets are **masked** in all shiprig output and fetched **just-in-time**
(cached once per ref per run, so a 1Password biometric prompt happens at most
once per secret).

## Quick reference

| | npm | crates.io | NuGet.org |
|---|---|---|---|
| config block | `npm` | `cargo` | `dotnet` |
| OIDC switch | `oidc: "auto"｜"off"` | same | same |
| secret-ref | `auth: "op://…"` | same | same |
| extra for OIDC | — | — | `user: "<nuget username>"` |
| env fallback | `NPM_TOKEN` | `CARGO_REGISTRY_TOKEN` | `NUGET_API_KEY` |

Config lives in `.changeset/release.jsonc` under the per-ecosystem block.

## OIDC trusted publishing (CI)

No stored secret, ephemeral credentials, and (npm, public repos) automatic
provenance. One-time, register your release workflow as a Trusted Publisher on
the registry:

- **npm** — npmjs.com → package → Settings → Trusted Publisher
- **crates.io** — crates.io → crate → Settings → Trusted Publishing
- **NuGet.org** — nuget.org → account → Trusted Publishing (then set
  `dotnet.user` to the policy creator's username)

Then grant the workflow an id-token and drop the token secret:

```yaml
permissions:
  contents: write
  id-token: write            # lets the job mint an OIDC token
steps:
  - uses: actions/checkout@v4
  - uses: actions/setup-node@v4           # npm: publish to the public registry
    with: { registry-url: https://registry.npmjs.org }
  - run: shiprig publish --yes
    env:
      GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
      # no NPM_TOKEN / CARGO_REGISTRY_TOKEN / NUGET_API_KEY needed
```

GitLab works too: configure an `id_tokens` entry with
`aud: npm:registry.npmjs.org` (npm) / the registry's audience and shiprig picks
up `NPM_ID_TOKEN`. To force a token instead of OIDC, set `<eco>.oidc: "off"`.

> npm provenance needs npm ≥ 11.5.1 on the runner; auth itself does not, so an
> older npm still publishes (without the attestation).

## 1Password and other secret managers (local / non-OIDC)

When you publish from a laptop — or any context without a CI OIDC identity —
point shiprig at a secret instead of exporting a long-lived token. Three schemes:

```jsonc
// .changeset/release.jsonc
{
  "npm":    { "auth": "op://CI/npm/token" },        // 1Password secret reference
  "cargo":  { "auth": "env:CARGO_REGISTRY_TOKEN" }, // an environment variable
  "dotnet": { "auth": "cmd:op item get nuget --fields apikey" } // any command's stdout
}
```

- **`op://vault/item/field`** — resolved with `op read`. Works with both the
  1Password desktop app (biometric unlock) and a **service account**
  (`OP_SERVICE_ACCOUNT_TOKEN`, ideal for headless/CI-adjacent runs) — no config
  difference; whichever `op` is set up to use.
- **`env:NAME`** — reads an environment variable (e.g. from your shell or a
  `.env`).
- **`cmd:…`** — runs a shell command and takes its stdout as the secret. Use this
  for anything `op read` can't express, or for **2FA/OTP**:
  `cmd:op item get npm --otp`.

One-off override without editing config (npm only):
`shiprig publish --npm-auth op://CI/npm/token`.

### Check it before you publish

`shiprig init` preflights configured refs without resolving the secret (no
prompt, no value read) — so a missing `op` CLI or a signed-out session surfaces
early:

```
Publish auth (configured refs, never stored):
  ✓ npm     op://CI/npm/token — 1Password CLI signed in
  ⚠ NuGet   op://CI/nuget/key — 1Password: not signed in — run `op signin`
```

### Troubleshooting 1Password

shiprig maps the common `op` failures to actionable messages:

| Symptom | Message | Fix |
|---|---|---|
| `op` not on PATH | *1Password CLI `op` not found* | install the CLI, or use `env:`/`cmd:` |
| signed out | *not signed in to 1Password* | `op signin` locally, or set `OP_SERVICE_ACCOUNT_TOKEN` in CI |
| bad reference | *secret reference … not found* | check the `op://vault/item/field` path |

## Notes

- **No regressions:** with no `auth` and no OIDC context, each adapter uses its
  ambient credential exactly as before.
- **Custom/private registries:** OIDC is scoped to the public registries
  (registry.npmjs.org, crates.io, nuget.org). Private feeds use the secret-ref or
  ambient path.
- Arbitrary release steps (not just publish) can also pull from a secret manager
  via the pipeline's lazy `vars` (`{"command": ["op", …], "lazy": true}`), which
  are masked the same way.
