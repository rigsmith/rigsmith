# Fully-customizable release pipeline — demo

shiprig's release pipeline is **100% overridable**, knope-style: every built-in
step can be replaced, reordered, wrapped, or removed, and you can insert your own
steps anywhere. This demo proves it by replacing the *entire* pipeline with
`echo` commands, so you can watch every lifecycle slot fire without touching the
network, git, or a registry.

```sh
./run.sh
```

It spins up a throwaway two-package Node workspace (`@acme/web@2.1.0`,
`@acme/api@1.4.0`), drops in [`release.jsonc`](./release.jsonc), and runs the
pipeline twice — a `--dry-run` preview and a real `--yes` run. Uses the `shiprig`
on your `PATH`, or `SHIPRIG=/path/to/shiprig ./run.sh`, or builds one from the
repo if neither is present.

## What it shows

[`release.jsonc`](./release.jsonc) exercises every customization point:

- **Override every built-in** — `version`, `commit`, `build`, `publish`, `tag`,
  `push`, `release`, `issues` are each replaced by a custom `run`. The three
  native steps (`build`/`release`/`issues`) print a *"custom run replaces the
  native step"* note in the plan.
- **Insert custom steps** anywhere in `order` — `preflight`, `node-check`,
  `dotnet-check`, `announce`.
- **Wrap any step** with `before` / `after` commands.
- **Global hooks** — `hooks.before` / `after` / `onError` bracket the whole run.
- **Built-in variables** — `${versions}`, `${version.web}`, `${tags}`,
  `${releaseUrls}`, `${issues}` interpolate into commands.
- **Captured + masked secret** — a `lazy` `${vars.otp}` is fetched right before
  `publish` and printed as `***`.
- **Confirm gates** — `publish` and `push` gate the run (bypassed by `--yes`).
- **Ecosystem targeting** — `node-check` runs (Node packages present);
  `dotnet-check` is **skipped with a reason** (no .NET packages).
- **Display names** — `Preflight`, `Node check`, `.NET check`, `Announce`.
- **Dry-run control** — only `publish` opts into executing in `--dry-run` (its
  `dryRun` alternate), everything else is preview-only.

## `shiprig release --dry-run`

The plan is rendered with built-in variables filled in; `${vars.*}` and
`${releaseUrl*}` show as `‹…›` placeholders (resolving them has side effects), and
only the one `dryRun` command actually executes:

```text
Release plan (dry run - only dryRun-marked commands run):
  - Preflight
      echo '[preflight] preparing @acme/api@1.4.0, @acme/web@2.1.0'
  - version
      before: echo '  [version.before] computing bumps'
      run: echo '  [version] bump → @acme/api@1.4.0, @acme/web@2.1.0'
      after: echo '  [version.after] CHANGELOG written'
  - commit
      run: echo '  [commit] git commit -m "release @acme/api@1.4.0, @acme/web@2.1.0"'
  - build
      before: echo '  [build.before] lint'
      note: custom run replaces the native step (build distributable artifacts skipped)
      run: echo '  [build] package artifacts for @acme/api@1.4.0, @acme/web@2.1.0'
      after: echo '  [build.after] artifacts ready'
  - Node check
      echo '[node-check] npm test for 2.1.0'
  - .NET check (no dotnet packages in this release)
  - publish
      before: echo '  [publish.before] auth'
      confirm: Publish ${versions}?
      run: echo '  [publish] publish @acme/api@1.4.0, @acme/web@2.1.0 --otp ‹vars.otp›'
      after: echo '  [publish.after] published'
  - tag
      run: echo '  [tag] git tag @acme/api@1.4.0, @acme/web@2.1.0'
  - push
      confirm: Proceed with the 'push' step?
      run: echo '  [push] git push --follow-tags'
  - release
      before: echo '  [release.before]'
      note: custom run replaces the native step (per-package forge release skipped)
      run: echo '  [release] forge release for @acme/api@1.4.0, @acme/web@2.1.0'
      after: echo '  [release.after] urls: ‹releaseUrls›'
  - issues
      note: custom run replaces the native step (comment on / close resolved issues skipped)
      run: echo '  [issues] comment/close '
  - Announce
      echo '[announce] shipped @acme/api@1.4.0, @acme/web@2.1.0'

==> publish
    $ echo '  [publish] DRY — would publish @acme/api@1.4.0, @acme/web@2.1.0'
      [publish] DRY — would publish @acme/api@1.4.0, @acme/web@2.1.0
ok publish
Release complete. dry run - plan previewed, only dryRun-marked commands ran
```

## `shiprig release --yes`

Every step runs (all echoes), in order — `before` → action → `after` per step,
bracketed by the global hooks. `.NET check` is skipped; the lazy OTP is captured
at `publish` and masked as `***`:

```text
    $ echo '╭─ [hooks.before] releasing @acme/api@1.4.0, @acme/web@2.1.0'
    ╭─ [hooks.before] releasing @acme/api@1.4.0, @acme/web@2.1.0
==> Preflight
    [preflight] preparing @acme/api@1.4.0, @acme/web@2.1.0
ok Preflight
==> version
      [version.before] computing bumps
      [version] bump → @acme/api@1.4.0, @acme/web@2.1.0
      [version.after] CHANGELOG written
ok version
   … commit, build (before/run/after) …
==> Node check
    [node-check] npm test for 2.1.0
ok Node check
--- .NET check skipped (no dotnet packages in this release)
==> publish
      [publish.before] auth
      [publish] publish @acme/api@1.4.0, @acme/web@2.1.0 --otp ***
      [publish.after] published
ok publish
   … tag, push, release (before/run/after), issues …
==> Announce
    [announce] shipped @acme/api@1.4.0, @acme/web@2.1.0
ok Announce
    ╰─ [hooks.after]  done: @acme/api@1.4.0, @acme/web@2.1.0
Release complete.
```

`${releaseUrls}`/`${issues}` print empty here because the throwaway repo has no
forge remote and no issue-referencing commits; against a real GitHub repo they
resolve to the release URLs and issue numbers. To see `onError` fire, change any
step's `run` to `exit 1` and re-run.
