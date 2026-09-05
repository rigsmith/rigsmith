---
type: fix
scope: clauderig
"github.com/rigsmith/rigsmith"
---

The secret tripwire no longer refuses a sync over ordinary English.

1.14.0 began scanning the complete staged stream for credential signatures — the right change, and it immediately found that the signatures themselves were never anchored. `sk-` is the tail of **task-**, **risk-** and **desk-**, so `global-task-runner-configuration` read as an OpenAI key; `AKIA` and `ASIA` matched anywhere inside a longer uppercase or base64 run, so a shouted phrase did too. The rules had only ever been used to rewrite tokens the tool was confident about; 1.14.0 promoted them to deciding whether a backup is allowed to happen at all.

On one real machine that was **96 of 134 findings**, and the sync had been refusing since — which means the machine was not being backed up, by a check meant to protect it.

Every rule is now anchored on a word boundary, and an AWS access key id is matched at its real length of twenty characters rather than open-ended. A hyphenated run of lowercase words is also dropped: no vendor mints a key without a digit or a capital in it, so a body that is nothing but lowercase and hyphens is a sentence.

On that same machine the findings go from 134 files to 26 — and those 26 do carry credential-shaped values, which is the answer the check exists to give.
