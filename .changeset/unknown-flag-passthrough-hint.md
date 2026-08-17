---
type: feat
"github.com/rigsmith/rigsmith"
---

An unknown flag now names itself and hands back the `--` form of your own command line.

`rig build --target=brave_browser_tests` in a Node repo ended at `Unknown flag: --target.` and nothing else. The flag was named — pflag has always done that — but the way out wasn't: rig forwards anything after `--` to the ecosystem command, and no error, no `--help` screen and no page of the docs said so. The escape hatch existed and was unreachable except by guessing.

The error now answers both questions it used to leave open — was that a typo, and how do I pass it through:

```
  ERROR

  Unknown flag: --target.

  rig build doesn't take --target. To pass it to the underlying command, put it after --:

      rig build -- --target=brave_browser_tests
```

The suggested line is the user's real command line with `--` inserted at the flag that failed, not a generic example, so it can be pasted as typed — quoting included, and with the tokens rig did understand left in front of the separator (`rig build -aZ` suggests `rig build -a -- -Z`, keeping `-a` as `--all`). Inserting at the *first* unrecognized flag means one edit fixes a line carrying several of them.

Unknown flags still error rather than forwarding automatically. Forwarding would mean `rig build --dry-runn` silently runs a build with no dry run, and rig's own flags would become the special case needing an escape hatch instead of the ecosystem's. The cost of erroring was never the rule, it was that the rule was undiscoverable — so a near-miss of a real flag is now called out as one (`Did you mean --dry-run?`), and the passthrough form is offered alongside it in case it wasn't a typo. The budget for "near miss" scales with the flag's length: two edits over a short name is a different flag, not a typo of one, and a confident wrong guess is worse than none.

Applies to every verb that appends what it doesn't consume to the ecosystem command — `build`, `test`, `run`, `format`, `lint`, `typecheck`, `clean`, `rebuild`, `install`, `ci`, `add`, `global`, `dlx`. A verb that assembles its own argv (`coverage`, `publish`, `worktree …`) keeps pflag's plain error rather than promising a `--` it cannot honour; those still get the did-you-mean. Each forwarding verb also states the convention in its own `--help`, so `--` is findable before you need it:

```
  EXAMPLES

    # flags rig doesn't own go to the underlying command after --
    rig build -- --verbose
```

Two supporting fixes make the suggested line true wherever it is printed:

- Args after `--` are no longer read as a selector. `rig test -- --logger=trx` in a .NET repo used to hand `--logger=trx` to the test-class matcher as if it were a class name; a verb now takes its project/class argument only from the tokens ahead of the separator.
- `core/fang` renders a multi-line error with its layout intact — the headline as the error sentence, the rest unwrapped — instead of reflowing every line to the terminal width, which would break a command line the reader is meant to copy.
