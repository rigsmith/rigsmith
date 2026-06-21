---
"github.com/rigsmith/rigsmith": minor
---

rig custom commands gain a Tengo `script` form for cross-platform command bodies with real logic. Alongside the shell-string and argv forms, a `.rig.json` command can now be a script:

```jsonc
"commands": {
  "release": {
    "script": [
      "mkdir(`-p`, `dist`)",
      "log(`building for ` + ctx.os)",
      "if ctx.ecosystem == `go` { sh(`go build ./...`) }",
      "sh(`tar czf dist/app.tgz dist/app`)"
    ]
  },
  "clean": { "script": { "file": "./scripts/clean.tengo" } }
}
```

The script runs through the shared `core/script` runtime with `sh()`, `cp()`/`mv()`/`rm()`/`mkdir()`, `log()`, and `fail()` builtins — `sh()` and the file ops go through the portable shell by default, so the body is cross-platform (use `"shell": "system"` to opt `sh()` into the OS shell). A `ctx` object exposes `args`, `env`, `root`, `cwd`, `ecosystem`, and `os`. `--dry-run` previews side effects. Accepts a string, an array of lines, or `{ "file": "path.tengo" }` (resolved relative to the config; a non-`.tengo` extension still loads but is flagged as a likely typo).
