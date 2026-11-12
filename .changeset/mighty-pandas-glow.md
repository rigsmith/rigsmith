---
type: feat
scope: rig
"github.com/rigsmith/rigsmith"
---

rig can run your code with its secrets from Doppler. Add `"doppler": { "project": "…", "dev": "dev", "ci": "ci", "release": "release" }` to `.rig.json` and `rig build`, `test`, `run`, `rebuild`, `verify`, `watch`, `coverage`, `publish` and custom commands run under `doppler run -p <project> -c <dev>`, shown in the `→` echo and in `-n`. If the doppler CLI is missing or signed out rig refuses and says to run `doppler login` instead of running without secrets; `--no-doppler` skips it for one run, and in CI rig wraps nothing. `rig secrets` and `rig info` show the project and configs, and `rig secrets ci NAME...` prints the GitHub Actions steps that fetch a job's secrets (the fetch action pinned by commit, no injected env vars) and fail the job when the token or any secret is missing.