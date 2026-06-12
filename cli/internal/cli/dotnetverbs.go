// The pure verb logic ported from the .NET rig (RunVerb / TestVerb /
// PublishVerb / AddVerb / RemoveVerb / GlobalVerb / DlxVerb / UpdateVerb /
// OutdatedVerb / RebuildVerb / Exec.WinCmdArguments / TestPlatform): project
// resolution and argv building as pure, unit-tested functions, kept apart from
// the cobra command wiring so a verb's behavior can be verified without
// spawning a process.
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
)

// projectResolution is the outcome of picking one project: exactly one of
// Selected (a single match), Ambiguous (the caller prompts/errors), or Err.
type projectResolution struct {
	Selected  *detect.ProjectInfo
	Ambiguous []detect.ProjectInfo
	Err       string
}

// resolveRunProject picks the project for run/publish: only runnable projects
// count; with no query the defaultProject wins, then a sole runnable, else
// ambiguous; a query resolves exact Name/ShortName first, then substring.
// Mirrors the .NET rig's RunVerb.Resolve. Pure.
func resolveRunProject(projects []detect.ProjectInfo, query, defaultProject string) projectResolution {
	var runnable []detect.ProjectInfo
	for _, p := range projects {
		if p.IsRunnable() {
			runnable = append(runnable, p)
		}
	}
	if len(runnable) == 0 {
		return projectResolution{Err: "no runnable projects found"}
	}

	if query == "" {
		if defaultProject != "" {
			for i := range runnable {
				if projectNameIs(runnable[i], defaultProject) {
					return projectResolution{Selected: &runnable[i]}
				}
			}
		}
		if len(runnable) == 1 {
			return projectResolution{Selected: &runnable[0]}
		}
		return projectResolution{Ambiguous: runnable} // ambiguous → caller decides
	}

	matches := findProjectMatches(runnable, query)
	switch len(matches) {
	case 0:
		return projectResolution{Err: "no project matches '" + query + "'"}
	case 1:
		return projectResolution{Selected: &matches[0]}
	default:
		return projectResolution{Ambiguous: matches}
	}
}

// resolveAddTarget picks the project to add/remove a package on: an explicit
// query, else the default project, else the sole project, else ambiguous.
// Unlike run, this spans ALL projects (packages go on libraries and tests
// too). Mirrors the .NET rig's AddVerb.ResolveTarget. Pure.
func resolveAddTarget(projects []detect.ProjectInfo, query, defaultProject string) projectResolution {
	if len(projects) == 0 {
		return projectResolution{Err: "no projects found"}
	}

	if strings.TrimSpace(query) != "" {
		matches := findProjectMatches(projects, query)
		switch len(matches) {
		case 0:
			return projectResolution{Err: "no project matches '" + query + "'"}
		case 1:
			return projectResolution{Selected: &matches[0]}
		default:
			return projectResolution{Ambiguous: matches}
		}
	}

	if defaultProject != "" {
		for i := range projects {
			if projectNameIs(projects[i], defaultProject) {
				return projectResolution{Selected: &projects[i]}
			}
		}
	}

	if len(projects) == 1 {
		return projectResolution{Selected: &projects[0]}
	}
	return projectResolution{Ambiguous: projects}
}

// projectNameIs reports an exact (case-insensitive) Name or ShortName match.
func projectNameIs(p detect.ProjectInfo, q string) bool {
	return strings.EqualFold(p.Name, q) || strings.EqualFold(p.ShortName(), q)
}

// findProjectMatches resolves a query against projects: exact Name/ShortName
// matches win; otherwise every project whose Name contains the query.
func findProjectMatches(projects []detect.ProjectInfo, query string) []detect.ProjectInfo {
	q := strings.TrimSpace(query)
	var exact, sub []detect.ProjectInfo
	for _, p := range projects {
		switch {
		case projectNameIs(p, q):
			exact = append(exact, p)
		case strings.Contains(strings.ToLower(p.Name), strings.ToLower(q)):
			sub = append(sub, p)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return sub
}

// buildDotnetRunArgs is the `dotnet [watch] run …` argument list. Framework /
// launch-profile slot in BEFORE the `--` forwarding boundary. Pure.
func buildDotnetRunArgs(projectFullPath, configuration, framework, launchProfile string, forwarded []string, watch bool) []string {
	args := []string{"run", "--project", projectFullPath}
	if configuration != "" {
		args = append(args, "-c", configuration)
	}
	if framework != "" {
		args = append(args, "--framework", framework)
	}
	if launchProfile != "" {
		args = append(args, "--launch-profile", launchProfile)
	}
	if len(forwarded) > 0 {
		args = append(args, "--")
		args = append(args, forwarded...)
	}
	if watch {
		args = append([]string{"watch"}, args...) // dotnet watch run …
	}
	return args
}

// dotnetTestRunner is which `dotnet test` CLI grammar applies. The SDK ships
// two distinct parsers and global.json's test.runner alone selects between
// them (a project's own MTP props do NOT switch the grammar).
type dotnetTestRunner int

const (
	vsTestRunner dotnetTestRunner = iota // positional project, --collect
	mtpRunner                            // --project, -- --coverage
)

const mtpRunnerName = "Microsoft.Testing.Platform"

// detectDotnetTestRunner resolves the runner: an explicit override wins
// ("mtp" → MTP; "xplat"/"vstest" → VSTest); otherwise the nearest global.json
// at or above root decides. Mirrors the .NET rig's TestPlatform.Detect.
func detectDotnetTestRunner(root, configured string) dotnetTestRunner {
	if strings.EqualFold(configured, "mtp") {
		return mtpRunner
	}
	if strings.EqualFold(configured, "xplat") || strings.EqualFold(configured, "vstest") {
		return vsTestRunner
	}
	if usesMtpCli(root) {
		return mtpRunner
	}
	return vsTestRunner
}

// usesMtpCli reports whether the nearest global.json at or above root opts
// into the Microsoft.Testing.Platform `dotnet test` parser. Tolerant: a
// missing/garbled file (or no test.runner) means classic VSTest.
func usesMtpCli(root string) bool {
	for dir := root; ; {
		path := filepath.Join(dir, "global.json")
		if data, err := os.ReadFile(path); err == nil {
			var doc struct {
				Test struct {
					Runner string `json:"runner"`
				} `json:"test"`
			}
			if json.Unmarshal(data, &doc) != nil {
				return false // unreadable global.json → classic VSTest
			}
			return strings.EqualFold(doc.Test.Runner, mtpRunnerName)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// buildDotnetTestArgs is the `dotnet [watch] test …` argument list. The
// project arg form follows the runner's CLI grammar: classic VSTest takes it
// positionally (it has no `--project` switch — passing one trips MSB1001),
// MTP takes `--project`. `--filter <expr>` is shared. Pure.
func buildDotnetTestArgs(runner dotnetTestRunner, testProject, filter, framework string, forwarded []string, watch bool, configuration string) []string {
	var args []string
	if runner == mtpRunner {
		args = []string{"test", "--project", testProject}
	} else {
		args = []string{"test", testProject}
	}
	if configuration != "" {
		args = append(args, "-c", configuration)
	}
	if framework != "" {
		args = append(args, "--framework", framework)
	}
	if filter != "" {
		args = append(args, "--filter", filter)
	}
	args = append(args, forwarded...)
	if watch {
		args = append([]string{"watch"}, args...) // dotnet watch test …
	}
	return args
}

// testShorthandFilter maps a leading MSTest filter operator (~ = !~ !=) to a
// full FullyQualifiedName filter, or "" when the token isn't shorthand. Pure.
func testShorthandFilter(token string) string {
	if token == "" {
		return ""
	}
	if token[0] == '~' || token[0] == '=' {
		return "FullyQualifiedName" + token
	}
	if len(token) > 1 && token[0] == '!' && (token[1] == '~' || token[1] == '=') {
		return "FullyQualifiedName" + token
	}
	return ""
}

// ---- rebuild guards (RebuildVerb.IsSkipped / IsWithinRoot) ----

// rebuildIsSkipped reports whether a (root-relative) directory matches the
// rebuild skip-list, by exact path-segment match or segment prefix. Pure.
func rebuildIsSkipped(relativeDir string, skip []string) bool {
	norm := strings.ReplaceAll(relativeDir, "\\", "/")
	for _, raw := range skip {
		s := strings.TrimSuffix(strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/")), "/")
		if s == "" {
			continue
		}
		if strings.EqualFold(norm, s) || hasPrefixFold(norm, s+"/") {
			return true
		}
	}
	return false
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// rebuildIsWithinRoot is true when path resolves strictly inside root (no
// `..` escape, a different tree, or the root itself). Pure — guards the
// recursive delete against a stray target.
func rebuildIsWithinRoot(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel != "" && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// ---- Windows .cmd delegation hardening (Exec.WinCmdArguments) ----

// cmdMetaRe matches every cmd.exe metacharacter that must be caret-escaped.
var cmdMetaRe = regexp.MustCompile("[()\\[\\]%!^\"`<>&|;, *?]")

var (
	winQuoteRe         = regexp.MustCompile(`(\\*)"`)
	winTrailingSlashRe = regexp.MustCompile(`(\\*)$`)
)

// winCmdArguments is the cmd.exe argument string that runs a .cmd/.bat shim
// with metacharacter-safe, caret-escaped arguments, so a forwarded arg can't
// break out and inject (cmd re-parses the line before the shim runs). Mirrors
// the .NET rig's Exec.WinCmdArguments / the Node tool's winCmdInvocation.
// Pure — testable on any OS.
func winCmdArguments(file string, args []string) string {
	escMeta := func(s string) string { return cmdMetaRe.ReplaceAllString(s, "^${0}") }
	escArg := func(arg string) string {
		s := winQuoteRe.ReplaceAllString(arg, `$1$1\"`)
		s = winTrailingSlashRe.ReplaceAllString(s, "$1$1")
		s = "\"" + s + "\""
		return escMeta(escMeta(s)) // double-escape for the .cmd/.bat case
	}
	parts := []string{escMeta(file)}
	for _, a := range args {
		parts = append(parts, escArg(a))
	}
	return `/d /s /c "` + strings.Join(parts, " ") + `"`
}

// ---- publish (PublishVerb) ----

// hostRid is the .NET runtime identifier for the host platform, the default
// when publish.rid isn't configured (RuntimeInformation.RuntimeIdentifier).
func hostRid() string {
	osPart := runtime.GOOS
	switch osPart {
	case "darwin":
		osPart = "osx"
	case "windows":
		osPart = "win"
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return osPart + "-" + arch
}

// resolvePublishRid picks the RID: the configured publish.rid, else the host's.
func resolvePublishRid(cfg *config.Publish) string {
	if cfg != nil && strings.TrimSpace(cfg.Rid) != "" {
		return cfg.Rid
	}
	return hostRid()
}

// resolvePublishOutput expands the output template (default "dist/{rid}"),
// substituting {rid}. Pure.
func resolvePublishOutput(cfg *config.Publish, rid string) string {
	template := "dist/{rid}"
	if cfg != nil && strings.TrimSpace(cfg.Output) != "" {
		template = cfg.Output
	}
	return strings.ReplaceAll(template, "{rid}", rid)
}

// buildPublishArgs is the `dotnet publish …` argument list. Pure.
func buildPublishArgs(projectPath, configuration, rid string, selfContained, singleFile bool, outputDir string) []string {
	return []string{
		"publish", projectPath,
		"-c", configuration,
		"-r", rid,
		"--self-contained", strconv.FormatBool(selfContained),
		"-p:PublishSingleFile=" + strconv.FormatBool(singleFile),
		"-o", outputDir,
	}
}

// ---- ni-parity dependency verbs (RemoveVerb / GlobalVerb / DlxVerb) ----

// buildRemovePackageArgs is `dotnet remove <project> package <pkg> [forwarded…]`. Pure.
func buildRemovePackageArgs(projectFullPath, pkg string, forwarded []string) []string {
	return append([]string{"remove", projectFullPath, "package", pkg}, forwarded...)
}

// buildGlobalToolArgs is `dotnet tool install --global <tool> [forwarded…]`. Pure.
func buildGlobalToolArgs(tool string, forwarded []string) []string {
	return append([]string{"tool", "install", "--global", tool}, forwarded...)
}

// buildDlxArgs is the `dnx` argv: the tool spec then any forwarded args. Pure.
func buildDlxArgs(tool string, forwarded []string) []string {
	return append([]string{tool}, forwarded...)
}

// dnxAvailable reports whether the `dnx` launcher (ships with the .NET 10 SDK)
// is resolvable on PATH. Pure-ish (reads PATH only, never throws).
func dnxAvailable() bool {
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = []string{".exe", ".cmd", ".bat", ""}
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		for _, ext := range exts {
			if _, err := os.Stat(filepath.Join(dir, "dnx"+ext)); err == nil {
				return true
			}
		}
	}
	return false
}

// ---- self-update version logic (UpdateVerb) ----

// latestStable returns the highest stable (non-prerelease) version in the
// list, or "" when none parses. Pure.
func latestStable(versions []string) string {
	best := ""
	var bestParts []int
	for _, v := range versions {
		if strings.Contains(v, "-") {
			continue // drop prereleases (e.g. 1.2.0-beta)
		}
		parts, ok := parseVersion(v)
		if !ok {
			continue
		}
		if best == "" || compareVersions(parts, bestParts) > 0 {
			best, bestParts = v, parts
		}
	}
	return best
}

// isNewer reports whether latest is newer than current. An unknown ("") or
// unparseable current is treated as "update available"; an unparseable latest
// is not. Build metadata / prerelease tags are stripped before comparing, so
// "1.1.0+sha" and "1.1.0" compare equal. Pure.
func isNewer(current, latest string) bool {
	l, ok := parseVersion(versionCore(latest))
	if !ok {
		return false
	}
	if current == "" {
		return true
	}
	c, ok := parseVersion(versionCore(current))
	if !ok {
		return true
	}
	return compareVersions(l, c) > 0
}

// versionCore strips build metadata / prerelease so cores compare equal.
func versionCore(v string) string {
	if i := strings.IndexAny(v, "+-"); i >= 0 {
		return v[:i]
	}
	return v
}

// parseVersion parses a dotted numeric version (at least major.minor, like
// System.Version). ok=false on any non-numeric component.
func parseVersion(v string) ([]int, bool) {
	fields := strings.Split(strings.TrimSpace(v), ".")
	if len(fields) < 2 {
		return nil, false
	}
	parts := make([]int, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return nil, false
		}
		parts[i] = n
	}
	return parts, true
}

// compareVersions compares componentwise; missing components count as 0.
func compareVersions(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
}

// siblingSelfUpdateArgs are the args for handing off to the sibling tool's
// self-update. Always carries --self-only so the sibling never cross-updates
// back to us (infinite mutual recursion). Pure.
func siblingSelfUpdateArgs(check bool) []string {
	if check {
		return []string{"self-update", "--check", "--self-only"}
	}
	return []string{"self-update", "--self-only"}
}

// ---- outdated (OutdatedVerb) ----

// buildOutdatedArgs is the `dotnet list [solution] package …` argument list.
// The three report lenses are mutually exclusive (vulnerable > deprecated >
// outdated); --include-prerelease only applies to the default outdated lens.
// Pure.
func buildOutdatedArgs(solution string, vulnerable, deprecated, transitive, prerelease bool, forwarded []string) []string {
	args := []string{"list"}
	if solution != "" {
		args = append(args, solution)
	}
	args = append(args, "package")

	switch {
	case vulnerable:
		args = append(args, "--vulnerable")
	case deprecated:
		args = append(args, "--deprecated")
	default:
		args = append(args, "--outdated")
	}

	if transitive {
		args = append(args, "--include-transitive")
	}
	if prerelease && !vulnerable && !deprecated {
		args = append(args, "--include-prerelease")
	}
	return append(args, forwarded...)
}
