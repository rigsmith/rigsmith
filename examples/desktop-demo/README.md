# Desktop-app release demo — Tauri & Electron

Two minimal "empty window" desktop apps that shiprig takes through a full release
cycle to a native installer. They show shiprig's **ecosystem overlays**: a desktop
app reuses its language's manifest for versioning but is *released* as installers
attached to a forge release, not pushed to a registry.

| Demo | Overlays | Version source | Build → binary |
|------|----------|----------------|----------------|
| [`tauri/`](./tauri) | `cargo` | `tauri.conf.json` + `Cargo.toml` (lockstep) | `cargo tauri build` → `.dmg`/`.app`/`.deb`/`.AppImage`/`.msi` |
| [`electron/`](./electron) | `node` | `package.json` | `electron-builder` → `.dmg`/`.AppImage`/NSIS `.exe` |

Each is a self-contained shiprig workspace (its own `.changeset/`). Run either:

```sh
cd tauri    && ./run.sh      # or: cd electron && ./run.sh
```

Each `run.sh` works on a throwaway copy in a tempdir (so these directories stay
clean) and:

1. **`shiprig status`** — shows the pending changeset and that the app is owned by
   the `tauri` / `electron` ecosystem (the overlay claimed it from cargo / node).
2. **`shiprig version`** — applies the bump. For Tauri this stamps **both**
   `tauri.conf.json` and `Cargo.toml` (lockstep, because the conf carries the
   version); for Electron it stamps `package.json`.
3. **`shiprig release --dry-build`** — runs only the build step (no registry,
   forge, or git side effects) so the framework's bundler produces the installer.

The `status` and `version` steps need no app toolchain — they only read and stamp
files, which is exactly what the adapters do — so they run anywhere. The build
step needs the framework's toolchain:

- **Tauri**: Rust + the Tauri CLI (`cargo install tauri-cli --version '^2'`) and
  your platform's [Tauri prerequisites](https://tauri.app/start/prerequisites/).
  `run.sh` generates placeholder icons so the bundle is self-contained.
- **Electron**: Node.js + npm (the script `npm install`s Electron and
  electron-builder into the temp copy on first run).

## Signing (optional)

Both are unsigned by default. To sign, add a `signing` block to the ecosystem in
`.changeset/config.json` — off unless `enabled`, secrets resolved via the same
`op://…` / `env:NAME` / `cmd:…` references as the publish `auth` key:

```jsonc
{
  "baseBranch": "main",
  "electron": {
    "signing": {
      "enabled": true,
      "env": {
        "CSC_LINK": "op://CI/apple-cert/base64",
        "CSC_KEY_PASSWORD": "op://CI/apple-cert/password"
      }
    }
  }
}
```

shiprig resolves and masks each secret and threads it into the build environment,
where electron-builder / Tauri pick up their standard signing variables (this is
the macOS path — `CSC_*` / `APPLE_*`, signed during packaging).

### Windows (Azure Trusted Signing)

Windows installers are signed by the post-build **`sign` step** (between `build`
and `release`), which runs over the produced `.exe`/`.msi`. Add a `windows` block;
Azure credentials go through `env` (resolved + masked, read by the `sign` CLI's
`DefaultAzureCredential`):

```jsonc
{
  "baseBranch": "main",
  "electron": {
    "signing": {
      "enabled": true,
      "windows": {
        "tool": "azure-trusted-signing",
        "endpoint": "https://wus2.codesigning.azure.net",
        "account": "my-signing-account",
        "certificateProfile": "my-profile"
      },
      "env": {
        "AZURE_TENANT_ID": "op://CI/azure/tenant",
        "AZURE_CLIENT_ID": "op://CI/azure/client-id",
        "AZURE_CLIENT_SECRET": "op://CI/azure/client-secret"
      }
    }
  }
}
```

This needs the dotnet `sign` CLI on PATH (`dotnet tool install --global sign`).
For a different signer (AzureSignTool, signtool, …) set `"tool": "command"` and a
`"command"` argv with `"{file}"` where the artifact path goes. Signing is off by
default; if a secret can't be resolved, the build proceeds **unsigned with a
warning** on a terminal but **fails** in CI.
