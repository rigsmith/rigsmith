# Claude artifact policy boundary

The next v2 extraction puts Claude's root and file classification in
`internal/clauderig/adapter`. Sync, restore, and conflict resolution consume these
decisions while their existing filesystem, codec, and Git implementations remain
in place. This is an internal Claude policy package, not a public adapter API or
a shared implementation for both vendors.

## Responsibilities

| Entry point | Contract |
| --- | --- |
| `Roots` | Preserve configured roots, order, locations, and enabled flags; append Desktop profiles using Desktop's enabled setting and the existing portable profile path. |
| `Root.Allowlist` | Supply the unchanged Claude include/exclude rules to the live walk and staged reconciliation. Unknown root IDs retain the existing CLI rules. |
| `Root.Classify` / `Classify` | Describe a root-relative file's kind, retention, transform, chunk eligibility, Desktop keep-only keys, and merge selection. Classification does not authorize a path or perform I/O. |
| `ClassifyMerge` | Select the existing metadata union, text union, or newest-snapshot policy for a backup-repository-relative path. Keep chunk-index checks and record deduplication explicit. |
| `NewFlushScope` | Resolve native hook paths and select each named transcript plus its sibling session directory, including subagents. An empty selection covers nothing. |

File paths are slash-separated and root-relative; merge paths include the backup
root prefix. Flush paths are native filesystem paths and retain the existing
symlink resolution. Discovery of local/staged Desktop profiles, source/target
overrides, account observation, hook decoding, and explicit normal/selected/all
flush intent stay with their existing callers.

## Deliberately separate policies

- Age retention applies to files under `projects/`, except the direct
  `projects/<slug>/memory/` subtree. Memory remains durable state. Other project
  files, including JSON and tool output, retain their current age policy.
- Capture chunking and large-file throttling recognize lowercase `.jsonl` under
  `projects/`, excluding memory. A skill's `.jsonl` stays ordinary copied data.
- Lowercase `.json` uses the existing structured redaction and path-transform
  pipeline. Other project files are candidates for optional conversation
  scrubbing; the content-based binary check still decides whether rewriting is
  safe. Classification alone never permits a binary rewrite.
- Desktop `config.json`, including profile `data/config.json`, keeps only the
  existing stable preference keys. Profile metadata and other JSON retain their
  existing treatment. Code-session sidecars remain distinct from Cowork sidecars.
- Claude's merge policy has a broader legacy rule: `.jsonl` is unioned regardless
  of location and case, while prose is unioned only inside a `memory/` directory.
  The lowercase chunk-index check, metadata filenames, deduplication, and fallback
  behavior are preserved. These rules must not become generic defaults for Codex.

The allowlist matcher, serializers, redactor, chunk representation, symlink and
prune protections, metadata readers, permissions, and backup layout are unchanged.
Restore continues to handle older staged files independently of the current live
allowlist. No migration, configuration option, hook change, queue, or worker is
introduced.

## Validation and next step

Direct adapter tests pin the policy distinctions, Desktop/profile exclusions,
root ordering and enablement, and session/subagent flush coverage through native
path aliases. Existing engine and merge tests exercise the wiring. Synthetic
end-to-end tests and the [fixed CLI compatibility baseline](CLAUDERIG-V2-COMPATIBILITY.md)
remain the cross-platform gate; the baseline is unchanged.

Next, extract file processing and restore mechanics using these explicit
decisions. Define shared interfaces at their concrete consumers and keep Claude
codecs and native metadata interpretation behind Claude implementations. Store
coordination and the durable queue remain later, separately tested milestones.
