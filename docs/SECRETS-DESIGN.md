# Secrets managers (Doppler) — design

How rig and shiprig read secrets from a secrets manager. Doppler is the first
provider; the seam is shaped so a second (Infisical, say) is one more file in
`core/secrets` and one more `.rig.json` key, with no change to the verbs.

## The layout this matches

- One Doppler workplace, one **project** per product (`foxcraft`, `owlwatch`).
- **Configs by purpose:** `dev` for local runs, `ci` for GitHub Actions,
  `release` for publishing (feed keys, signing material, notary credentials).
- **CI:** each repo holds one GitHub secret, `DOPPLER_TOKEN` — a read-only
  service token for its `ci` config. A job fetches once and each later step takes
  only the secret it needs from `steps.<id>.outputs.<NAME>`. Never a job-level
  `env:`.
- **Locally:** the project and config are named on every call
  (`doppler run -p <project> -c <config> -- …`). `doppler setup` writes per-folder
  state, and every git worktree is a new folder.

## Decisions

**One `.rig.json` block, keyed by provider.** `"doppler": { "project", "dev",
"ci", "release" }`. The key names the provider, so the block reads naturally and
a second provider is a sibling key; two blocks at once is an error. A repo's
block replaces a global (`~/.rig.json`) one whole, never field by field — a repo
must not inherit another product's `release` config. Names are validated as
plain identifiers because each lands after a flag on a command line.

**Absent is the only opt-out.** A block that is present but wrong (no project, a
flag-shaped config name) is an error everywhere — rig, `rig secrets`, shiprig —
never "not configured". The failure this guards against is the one that started
it: an empty Postgres connection let every Postgres test lane skip while the run
showed green.

**rig wraps; it does not fetch, where it can avoid it.** Commands rig execs run
as `doppler run -p P -c dev -- argv`, so the secrets exist only in that child's
environment and the `→` echo is the real command line. Commands rig interprets
in-process (portable-shell lines, Tengo scripts) have no child to wrap; for those
rig reads the dev config once (`doppler secrets download --no-file`) and lays
the values over that command's environment, echoing `[doppler P/dev] <line>`. A
script's `ctx.env` does not get them.

**Which verbs.** The ones that build or run the project's code — build, test,
run, rebuild, verify, watch, coverage, publish — plus every custom command,
package script and Go script verb, since rig can't know what those do.
Formatting, linting, installs and housekeeping are never wrapped, so they never
demand a sign-in.

**Refuse, don't degrade.** Locally, a missing CLI or a CLI with no token for the
folder (`DOPPLER_TOKEN`, or the token `doppler login` stored) stops the run with
the fix. The check is local and cheap; a token Doppler has revoked fails at the
fetch with Doppler's own error — still never a run without secrets.
`--no-doppler` skips it once, deliberately. A dry run checks nothing and prints
the wrapped line.

**CI: rig wraps nothing.** In CI (`GITHUB_ACTIONS=true` or a truthy `CI`) the
workflow is the source: each step gets its secrets from the fetch step's outputs.
Wrapping there would need the CLI and a broader token in every job.

**CI fetch: a generator, not a composite action.** A composite action must
declare its outputs statically in `action.yml`, so it cannot return
`steps.<id>.outputs.<NAME>` for names it doesn't know. Its ways out are a single
JSON output (a mistyped `fromJSON(…).NAME` is silently empty) or `$GITHUB_ENV`
(every secret in every later step's environment — the job-level `env:` problem
again). `rig secrets ci NAME...` instead emits, inline in the job:

1. a token check that fails, naming the secret to add, when `DOPPLER_TOKEN` is
   unset;
2. `dopplerhq/secrets-fetch-action` pinned by commit
   (`451892f16195f9ac360e1a5bcbf0b5fd0e957534`, v2.0.0), without
   `inject-env-vars`;
3. a guard that fails when the token reads a project/config other than the
   `ci` one, or any named secret is missing or empty. It sees
   `steps.<id>.outputs.NAME != ''` — a boolean — never a value.

Generated agentic workflows (`gh aw`, `*.lock.yml`) read GitHub secrets
directly and are out of scope.

**shiprig: `doppler:NAME` is a secret reference.** It joins `op://`, `env:` and
`cmd:` in `core/auth`, so publish `auth`, `signing.env` and release-var `secret`
all take it with no per-site code. shiprig's root command puts a release-config
`Reader` in the command context; the reference reads through it. Locally that is
`doppler secrets get NAME --plain -p P -c release`; in CI it is `NAME` from the
step's environment (the generated fetch supplies it).

**The keychain as fallback, only before the move.** A release var may set
`command`/`os` beside a `doppler:` secret. The capture runs only while `.rig.json`
has no `release` config (and never in CI). Once the config exists, a missing
secret fails — falling back then would hide exactly the misconfiguration that
matters. Only a provider reference may carry a fallback.

**Names, never values.** Every source is reported by name
(`vars.key ← Doppler owlwatch/release (FEEDZ_API_KEY)`, `auth for dotnet: …`,
`sign app: APPLE_ID_PASSWORD ← …`). shiprig masks every value it resolves in
its own output; errors are built from names, scopes and the CLI's stderr —
never its stdout, and never a JSON decoder's message (which can quote the
input).

In CI, masking is the fetch action's, with two exceptions: it does not mask
Doppler's metadata outputs (`DOPPLER_PROJECT`, `DOPPLER_CONFIG`,
`DOPPLER_ENVIRONMENT`), and it does not mask a secret whose visibility is set
to `unmasked` in Doppler. A later step that prints one of those prints it in
clear. Keep credentials at Doppler's default (masked) visibility.

**Strict where lenient means "no secrets".** rig reads `.rig.json` leniently —
a malformed file degrades to defaults with a warning. The secrets decision
does not: rig and shiprig both use `secrets.LoadLayered`, which refuses a file
that can't be read, can't be parsed and names a provider, sets a provider
block twice (keys differing only by case), or has an unknown key inside the
block (`"releasee"`). A malformed file that names no provider keeps rig's
usual warn-and-continue.

## Not done

- No Infisical provider yet; `secrets.Provider` and the `providers` registry are
  where it goes.
- `rig secrets ci` prints steps; it does not rewrite workflow files in place.
- The readiness check does not call Doppler, so it cannot tell a revoked token
  from a good one; the fetch can.
