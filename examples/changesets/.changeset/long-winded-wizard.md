---
"@acme/cli": minor
---

feat: interactive `init` wizard

Running `acme init` now walks you through project setup instead of writing a
default config blindly. It:

- detects the package manager (npm / pnpm / yarn / bun) from the lockfile,
- prompts for the output directory (defaulting to `./dist`),
- offers to enable TypeScript, ESLint, and Prettier,
- writes a commented config so the choices are discoverable later, and
- prints the exact follow-up commands to run.

The old non-interactive behavior is still available with `acme init --yes`,
which accepts every default — handy for CI and scripts.

This summary is deliberately long so the changeset browser's detail view has
something to scroll.
