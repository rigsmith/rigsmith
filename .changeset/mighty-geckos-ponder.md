---
"github.com/rigsmith/rigsmith": patch
---

clauderig: `desktop add` now seeds a new profile from your existing Claude Desktop install, so it is usable immediately — MCP servers (`claude_desktop_config.json`), theme and locale come across, along with the small declarative config files. Nothing that carries the login does: `config.json` is rebuilt from the vetted portable keys rather than copied and filtered, so `oauth:tokenCache`, `oauth:tokenCacheV2` and `lastKnownAccountUuid` are absent by construction, and the session-state directories are never touched. A seeded profile still starts signed out. The vetted key list is now shared with `clauderig sync`'s config filter so the two cannot disagree about what is safe to copy. `--no-seed` starts from an empty profile.