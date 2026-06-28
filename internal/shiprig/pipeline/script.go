package pipeline

import (
	"sort"
	"strings"

	"github.com/rigsmith/rigsmith/core/script"
)

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
			"version": p.Version, "nextVersion": p.Version, "lastVersion": p.LastVersion,
			"tag": p.Tag, "changelog": p.Changelog,
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
		ctx["nextVersion"] = pkgs[0].Version
		ctx["lastVersion"] = pkgs[0].LastVersion
		ctx["tag"] = pkgs[0].Tag
		ctx["changelog"] = pkgs[0].Changelog
	}
	return ctx
}

// evalScriptBool evaluates an `if` expression against the script ctx for
// truthiness, delegating to the shared core/script runtime.
func evalScriptBool(expr string, scriptCtx map[string]interface{}) (bool, error) {
	return script.EvalBool(expr, scriptCtx)
}

// evalScriptString evaluates a computed-variable expression and renders its
// result as a string, delegating to the shared core/script runtime.
func evalScriptString(expr string, scriptCtx map[string]interface{}) (string, error) {
	return script.EvalString(expr, scriptCtx)
}
