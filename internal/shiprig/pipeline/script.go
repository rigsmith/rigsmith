package pipeline

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/stdlib"
)

// scriptModules is the safe Tengo stdlib available via import(...) in `if`
// conditions and computed `vars`: string/format/math/json helpers, but NOT
// os/exec — these are pure expressions over ctx, with no side effects.
var scriptModules = []string{"text", "fmt", "math", "times", "rand", "json", "base64", "hex", "enum"}

// scriptGlobals are the builtin modules pre-bound as globals, so a one-line
// expression can call e.g. text.re_match / fmt.sprintf without an import.
var scriptGlobals = []string{"text", "fmt", "math", "times", "rand", "json", "base64", "hex"}

// scriptTimeout bounds a single evaluation so a runaway expression can't hang a
// release.
const scriptTimeout = 10 * time.Second

// buildScriptCtx assembles the `ctx` object exposed to scripts: the release's
// packages/versions/tags/issues, the dry-run flag, and the layered environment.
// Single-package releases also get scalar ctx.version/ctx.tag/ctx.changelog.
func buildScriptCtx(rc ReleaseContext, env map[string]string, dryRun bool) map[string]interface{} {
	envMap := make(map[string]interface{}, len(env))
	for k, v := range env {
		envMap[k] = v
	}

	ctx := map[string]interface{}{
		"dryRun":   dryRun,
		"env":      envMap,
		"packages": []interface{}{},
		"tags":     []interface{}{},
		"issues":   []interface{}{},
		"versions": "",
	}
	if rc == nil {
		return ctx
	}

	pkgs := rc.Packages()
	pkgList := make([]interface{}, 0, len(pkgs))
	tags := make([]interface{}, 0, len(pkgs))
	agg := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		pkgList = append(pkgList, map[string]interface{}{
			"name": p.Name, "key": p.Key, "ecosystem": p.Ecosystem,
			"version": p.Version, "tag": p.Tag, "changelog": p.Changelog,
		})
		tags = append(tags, p.Tag)
		agg = append(agg, p.Name+"@"+p.Version)
	}
	sort.Strings(agg)

	issues := []interface{}{}
	for _, iss := range rc.Issues() {
		issues = append(issues, iss.Number)
	}

	ctx["packages"] = pkgList
	ctx["tags"] = tags
	ctx["issues"] = issues
	ctx["versions"] = strings.Join(agg, ", ")

	if len(pkgs) == 1 {
		ctx["version"] = pkgs[0].Version
		ctx["tag"] = pkgs[0].Tag
		ctx["changelog"] = pkgs[0].Changelog
	}
	return ctx
}

// evalScriptExpr evaluates a Tengo expression against the script ctx and returns
// the resulting variable. The expression is wrapped in an assignment so a bare
// expression (the common case for `if`/computed vars) is what users write.
func evalScriptExpr(expr string, scriptCtx map[string]interface{}) (*tengo.Variable, error) {
	s := tengo.NewScript([]byte("__out__ := (" + expr + ")"))
	s.SetImports(stdlib.GetModuleMap(scriptModules...))
	for _, name := range scriptGlobals {
		if mod, ok := stdlib.BuiltinModules[name]; ok {
			_ = s.Add(name, &tengo.ImmutableMap{Value: mod})
		}
	}
	if err := s.Add("ctx", scriptCtx); err != nil {
		return nil, err
	}

	runCtx, cancel := context.WithTimeout(context.Background(), scriptTimeout)
	defer cancel()
	compiled, err := s.RunContext(runCtx)
	if err != nil {
		return nil, err
	}
	return compiled.Get("__out__"), nil
}

// evalScriptBool evaluates an expression for truthiness (Tengo's rules: non-zero
// numbers, non-empty strings/collections, and true are truthy).
func evalScriptBool(expr string, scriptCtx map[string]interface{}) (bool, error) {
	v, err := evalScriptExpr(expr, scriptCtx)
	if err != nil {
		return false, err
	}
	return v.Bool(), nil
}

// evalScriptString evaluates an expression and renders its result as a string
// (for a computed variable's value).
func evalScriptString(expr string, scriptCtx map[string]interface{}) (string, error) {
	v, err := evalScriptExpr(expr, scriptCtx)
	if err != nil {
		return "", err
	}
	switch x := v.Value().(type) {
	case string:
		return x, nil
	case nil:
		return "", nil
	default:
		return fmt.Sprintf("%v", x), nil
	}
}
