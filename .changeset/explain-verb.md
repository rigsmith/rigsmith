---
type: feat
"github.com/rigsmith/rigsmith"
---

`rig explain <verb>` prints what a verb resolves to — command, directory, environment, source — without running it.

A `.rig.json` custom command can be silently wrong in a way nothing catches. Two real ones, live for weeks in a Chromium fork:

```jsonc
"markers": "grep -rho 'sheepish-[a-z-]*' src | sort -u",
"unowned": "grep -rho 'sheepish-[a-z-]*' src | grep -v '^./sheepish/' | sort -u"
```

The first character class has no digits, so `sheepish-p3a-removal` was truncated to `sheepish-p` and counted as its own marker. The second pipes into a path filter that can never match, because `-h` upstream suppressed the filenames. Both exited 0 and printed a plausible list. Both were found by eye, long after the fact.

Neither is a config error — the JSON is valid and the shell is valid — so no amount of validating `.rig.json` would have caught either. What was missing was a cheap way to look at the resolved command and think about it for ten seconds:

```
$ rig explain markers
Verb
  name:    markers
  source:  custom command · /repo/.rig.json

Command
  runs:    grep -rho 'sheepish-[a-z-]*' src | sort -u
  shell:   portable · rig's in-process POSIX shell, same on every OS
  dir:     /repo

Environment
  API_TOKEN=abc      · .env / .env.local
  SHEEPISH_ROOT=src  · .rig.json env
  the rest is inherited from the current environment

  nothing ran — `rig markers` runs it
```

It covers every form a verb can take: the shell string after OS selection (with the shell that will interpret it — portable or the OS one, which decides what syntax works), the argv form (flagged as exec'd directly, since that is why a pipe in it would not do what it looks like), a Tengo script's source, a `package.json` script, a script directory, and the ecosystem conventions. Env is listed per variable with the layer that set it, and a `.env` value the ambient environment overrides is marked as such — the stated value is otherwise one the command never sees. Bare `rig explain` lists every verb the repo resolves.

**Resolution is not reimplemented.** `customPlan`, `nodeScriptPlan`, `goScriptPlan` and `ecosystemPlan` are now the single resolvers, and the run paths call them: `runCustom` resolves then executes what came back. The env layers are named in one place and feed both the environment a command gets and the listing explain prints. A test asserts the two agree by comparing explain's output against what the run path echoes under `--dry-run` — an explain that can drift from a run is worse than no explain, since it would describe a command nobody executes.

For the same reason, explain refuses rather than guesses. `coverage`, `rebuild`, `publish`, `outdated` and `upgrade` decide part of their command while running; an argument to a built-in verb selects a project or a test class at run time. In both cases explain says so, and where that verb's `--dry-run` prints an exact command it points there, since that goes through the real path. `info` and `outdated` have no such contract — one has no underlying command, the other runs its scans without echoing them — so explain says only that it cannot show a guaranteed answer.

Also in this change:

- **A `commands` entry shadowed by a built-in verb now warns on load.** Defining `"build"` in a Node repo was silently ignored: the built-in ran, the config appeared accepted, and the symptom read as rig malfunctioning rather than as a naming collision. It now says `"build" in /repo/.rig.json is a built-in rig verb, so that entry never runs — rename it (e.g. "build:custom")`, and `rig explain build` repeats it next to the verb, which is where someone asking the question will be.
- **Config parse warnings are surfaced at all.** A malformed file degraded to defaults, an unknown top-level key, a script file that wouldn't load — these were collected into `Config.Warnings` and then read by nobody, so a broken config looked like an accepted one. Some of them were good messages nobody had ever seen: an unknown key already came with `did you mean "commands"?` attached. They now print once per run, and `rig info` — the report about the config, and where you look when rig isn't doing what the config says — grows a `Warnings` section listing the same problems. Commands that report them in their own output (`info`, `explain`) skip the per-run notice rather than saying it twice, and completion requests are exempt because a shell parses that output.
