# Tengo in 5 minutes (for JS/TS developers)

shiprig uses [Tengo](https://github.com/d5/tengo) for release-script logic
(`script` steps, `if` conditions, computed `vars`). It's a small, embeddable,
Go-flavoured scripting language — and the good news for JS folks is it's **closer
to JavaScript than to Go**: it has ternaries, truthy/falsy conditions,
`for x in list`, closures, and JSON-style object literals. You'll be productive
in a few minutes.

This is the doc Tengo's own site doesn't ship: a JS→Tengo quickstart.

## The 7 things to adjust coming from JS

1. **Declare with `:=`, reassign with `=`.** `let x = 1` → `x := 1`; then `x = 2`.
   There's no `let`/`const`/`var`, and you *must* use `:=` the first time.
2. **No template literals.** Backtick strings are *raw* (no `${}`). Use
   `fmt.sprintf("hi %s", name)` or `+` concatenation.
3. **Functions are values.** No `function` keyword, no `=>`. Write
   `f := func(a, b) { return a + b }`. Closures work as you'd expect.
4. **One nil: `undefined`.** No separate `null`. Missing map keys, no-return
   functions, and failed conversions all yield `undefined`.
5. **No methods on arrays/strings.** `arr.map(...)`, `s.includes(...)` don't
   exist. Use the stdlib: `enum.map(arr, fn)`, `text.contains(s, "x")` — or a
   plain `for` loop.
6. **Errors are values, not exceptions.** No `throw`/`try`/`catch`. `error("msg")`
   makes an error value; check it with `is_error(x)`. (In shiprig you'll mostly
   call the injected `fail("msg")`.)
7. **No classes/`this`.** Model data with maps; behaviour with closures.

Things that work just like JS: `if/else`, `c ? a : b`, `&&`/`||`/`!`, truthy/falsy
conditions (`0`/`""`/`undefined`/`false` are falsy), `for`, array/map literals,
`//` and `/* */` comments.

## Cheatsheet

| JavaScript / TypeScript | Tengo |
| --- | --- |
| `let x = 1` | `x := 1` |
| `x = 2` (reassign) | `x = 2` |
| `const f = (a, b) => a + b` | `f := func(a, b) { return a + b }` |
| `` `hi ${name}` `` | `fmt.sprintf("hi %s", name)` |
| `cond ? a : b` | `cond ? a : b` *(same)* |
| `if (x) {…} else {…}` | `if x {…} else {…}` *(no parens)* |
| `for (const x of arr)` | `for x in arr {…}` |
| `arr.forEach((x, i) => …)` | `for i, x in arr {…}` |
| `for (const [k, v] of Object.entries(o))` | `for k, v in o {…}` |
| `arr.length` / `Object.keys(o).length` | `len(arr)` / `len(o)` |
| `arr.push(x)` | `arr = append(arr, x)` |
| `arr.map(f)` / `arr.filter(f)` | `enum.map(arr, f)` / `enum.filter(arr, f)` |
| `arr.includes(x)` | `enum.any(arr, func(i, v) { return v == x })` |
| `{ a: 1, b: 2 }` | `{ a: 1, b: 2 }` *(same)* |
| `o.a` / `o["a"]` | `o.a` / `o["a"]` *(same)* |
| `null` / `undefined` | `undefined` |
| `typeof x` | `type_name(x)` |
| `JSON.stringify(o)` / `JSON.parse(s)` | `json.encode(o)` / `json.decode(s)` |
| `/(beta|rc)/.test(s)` | `text.re_match("(beta|rc)", s)` |
| `s.split(",")` | `text.split(s, ",")` |
| `s.startsWith("x")` | `text.has_prefix(s, "x")` |
| `console.log(x)` | `fmt.println(x)` |
| `throw new Error("x")` | `return error("x")` (check with `is_error`) |

Standard-library bits come from `import(...)`:

```go
fmt  := import("fmt")    // sprintf, println, printf
text := import("text")   // contains, split, has_prefix, re_match, to_upper, …
enum := import("enum")   // map, filter, each, any, all
json := import("json")   // encode, decode
```

Common builtins (no import): `len`, `append`, `delete`, `copy`, `type_name`,
`string`/`int`/`float`/`bool`, `is_error`, `is_undefined`, `error`.

## A real release script, both ways

*For each released package, pick an npm dist-tag from whether it's a prerelease,
copy the changelog in, then publish — skipping the publish in `--dry-run`.*

### JavaScript (for comparison)

```js
for (const pkg of ctx.packages) {
  const tag = /-(alpha|beta|rc)/.test(pkg.version) ? "next" : "latest";
  cp("CHANGELOG.md", `${pkg.dir}/CHANGELOG.md`);
  if (ctx.dryRun) { log(`would publish ${pkg.name}@${pkg.version} --tag ${tag}`); continue; }
  sh(`npm publish ${pkg.dir} --tag ${tag} --otp ${ctx.vars.otp}`);
}
```

### Tengo

```go
text := import("text")
fmt  := import("fmt")

for pkg in ctx.packages {
    tag := text.re_match("-(alpha|beta|rc)", pkg.version) ? "next" : "latest"

    cp("CHANGELOG.md", pkg.dir + "/CHANGELOG.md")

    if ctx.dryRun {
        log(fmt.sprintf("would publish %s@%s --tag %s", pkg.name, pkg.version, tag))
        continue
    }
    sh(fmt.sprintf("npm publish %s --tag %s --otp %s", pkg.dir, tag, ctx.vars.otp))
}
```

The only real differences: `:=`/`+` instead of template literals, `text.re_match`
instead of a regex literal, and `for pkg in` instead of `for…of`. Everything else
reads the same.

## In shiprig

Release scripts run **sandboxed** — Tengo gives you the language, and shiprig
injects a small, curated API (so a script can't reach arbitrary `os`/filesystem
by accident):

| Injected | What it does |
| --- | --- |
| `ctx` | the release: `ctx.packages` (`{name, key, ecosystem, version, tag, changelog}`), `ctx.versions`, `ctx.tags`, `ctx.issues`, `ctx.dryRun`, `ctx.env`, `ctx.vars` |
| `sh(cmd)` | run a shell command (through the portable cross-platform shell); returns stdout, fails the step on non-zero |
| `cp` / `mv` / `rm` / `mkdir` | cross-platform file ops (same Go implementation as the portable shell builtins) |
| `log(msg)` | write a line to the release output |
| `fail(msg)` | stop the step with an error |

In `--dry-run`, `sh`/`cp`/`mv`/`rm` are previewed (reported, not executed) while
the rest of the script still runs, so conditionals evaluate against the real
`ctx`.

> Heads up: the `script` / `if` / computed-`vars` integration is the upcoming
> Tengo step — this section describes the API it will expose. The language facts
> above are accurate today.

## Going deeper

- Official tutorial: <https://github.com/d5/tengo/blob/master/docs/tutorial.md>
- Standard library: <https://github.com/d5/tengo/blob/master/docs/stdlib.md>
- Builtins: <https://github.com/d5/tengo/blob/master/docs/builtins.md>
