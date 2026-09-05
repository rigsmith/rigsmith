---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Turning `redactTranscripts` on now scrubs the transcripts that were already synced.

It only ever applied to transcripts staged after the setting was enabled. Nothing about an already-staged copy changes when a setting does — same source, same timestamp — so the incremental skip declared it current and kept handing the unredacted copy back out on every restore, until the live transcript happened to change. Anyone who turned redaction on after noticing a key in a conversation was left with that key still in the repo. The first sync after it is enabled restages transcripts.

An opaque `Bearer` token is now redacted from transcripts too. clauderig already treated one as a credential when it found it in a settings file; a transcript was the one place it survived. The rule is bounded deliberately — token charset, at least 24 characters, and obvious placeholders like `Bearer YOUR_ACCESS_TOKEN` left alone — because `Bearer` appears in every API example anyone has pasted into a chat, and a wrong guess rewrites their conversation with no copy kept.
