---
type: fix
scope: shiprig
"github.com/rigsmith/rigsmith"
---

`${env.NAME}` now expands inside a captured var's command — `command` or any `os` entry, argv list or shell string — from the release environment, the way it already did in a literal's value and a step's `run`. It used to reach the command verbatim, so `["security", "find-generic-password", "-a", "${env.USER}", …]` asked the keychain for a user called `${env.USER}` and exited 44; with the var lazy and its value masked, that surfaced only at the push step, after the whole build, as "capture command for variable 'key' failed (exit code 44)" and nothing to look at. And a captured var whose command names an env var the release does not set now fails the release before any hook or step runs, in the dry run too, with a message naming both: `variable 'key' refers to ${env.USER}, which is not set in the release environment`. The check covers the captured vars the release will resolve — the eager ones, and the lazy ones a step in the plan or a hook refers to — so a lazy credential behind a disabled step stays optional, as before. A literal value is unchanged: an unset name there still expands to empty.
