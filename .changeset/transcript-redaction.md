---
type: feat
scope: clauderig
"github.com/rigsmith/rigsmith"
---

Secrets pasted into a conversation can now be scrubbed out of the synced copy, and every sync says which files it redacted.

A key in `settings.json` is a field, and the redactor has always caught those. A key you pasted into a chat is prose in the middle of a `.jsonl` transcript, and nothing caught it: the content rules stop at 64 KB and real transcripts run to megabytes, so they were never examined at all — the whole-file test also requires the file to be a single bare token, which no conversation is. A live key could reach the repo verbatim.

Set `"redactTranscripts": true` in `.clauderig.json` and credential-shaped tokens are replaced with a placeholder on the way into staging. **Your `~/.claude` is never modified** — clauderig backs a machine up, it does not edit it — so the secret stays exactly where you left it and simply stops leaving the machine. Off by default, because rewriting the middle of somebody's conversation is not something a backup tool should do uninvited.

The scrub is deliberately narrow: known vendor prefixes (Anthropic, OpenAI, GitHub, GitLab, Slack, AWS, Google) and JWTs, matched anywhere in a line. The generic high-entropy heuristic that backs the config redactor is *not* used here — a false positive there costs a redacted field nobody reads, while here it would rewrite the middle of a conversation with no copy of the original kept. A PEM private key is reported rather than rewritten: it spans a structure that cannot be edited safely, and half a scrubbed key is worse than a refusal that names the file. Transcripts are streamed line by line, so a 500 MB conversation costs a line of memory rather than 500 MB.

Separately, `N secrets redacted` now means what it says. Every JSON file is redacted on every pass, and the count was tallied at redaction time, so it reported the whole tree's total on every row — `21 secrets redacted` sat beside syncs that had written a single file, and beside the row above and below. It now counts only the files a run actually staged, so most rows say nothing about secrets and the ones that do are worth reading.

And the count names its files. Clicking a row in the window's Sync Activity feed lists what that run touched, with redacted files marked and the kind of credential found in each. The journal records paths and kinds only, never values — it is a map of where credentials turned up, which is worth having and is not itself a credential.
