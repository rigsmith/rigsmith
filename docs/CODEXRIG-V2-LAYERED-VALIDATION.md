# Codex layered validation: retired

The unfinished layered runtime validator was removed during the v2 scope reset.
Rigsmith no longer overlays or interprets external managed/system/project policy
for a restore, and no longer exposes versioned/layered preparation wrappers.
Codex remains responsible for its effective runtime configuration.

See [restore safety](CODEXRIG-V2-CONFIG-VALIDATION.md) for the current boundary and
[the delivery plan](CLAUDERIG-SHARED-LAYERS-ROADMAP.md) for remaining work.
The earlier implementation and rationale remain in Git history.
