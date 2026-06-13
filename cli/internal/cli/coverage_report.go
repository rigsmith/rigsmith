package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// ReportGenerator (https://github.com/danielpalme/ReportGenerator) is the
// de-facto cross-platform coverage report tool: it reads Cobertura, lcov, and
// many other formats and renders rich source-highlighted HTML. When it's
// present rig prefers it for every ecosystem whose output it can read; when
// it's absent rig falls back to a native per-ecosystem report.

// resolveReportGenerator decides how to invoke ReportGenerator, returning the
// command prefix (e.g. ["reportgenerator"] or ["dotnet","tool","run",...]) and
// whether RG is usable under the given mode (auto|off|install). It is the
// `resolve` hook of toolReportGenerator:
//
//   - mode "off"      → never (false);
//   - a `reportgenerator` on PATH (global tool)            → use it;
//   - a local tool-manifest entry (.config/dotnet-tools.json) → `dotnet tool run`;
//   - mode "install" with none of the above               → `dnx` (fetch on use);
//   - otherwise                                            → false (native fallback).
func resolveReportGenerator(root, mode string) ([]string, bool) {
	if mode == toolOff {
		return nil, false
	}
	if p, err := exec.LookPath("reportgenerator"); err == nil {
		return []string{p}, true
	}
	if manifestHasReportGenerator(root) {
		return []string{"dotnet", "tool", "run", "reportgenerator"}, true
	}
	if mode == toolInstall {
		if _, err := exec.LookPath("dnx"); err == nil {
			return []string{"dnx", "-y", "dotnet-reportgenerator-globaltool"}, true
		}
	}
	return nil, false
}

// manifestHasReportGenerator reports whether a dotnet tool manifest at or above
// root declares ReportGenerator (by package id or the `reportgenerator`
// command).
func manifestHasReportGenerator(root string) bool {
	dir := root
	for {
		for _, rel := range []string{filepath.Join(".config", "dotnet-tools.json"), "dotnet-tools.json"} {
			if toolManifestDeclaresReportGenerator(filepath.Join(dir, rel)) {
				return true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// toolManifestDeclaresReportGenerator parses a dotnet-tools.json and reports
// whether any tool is ReportGenerator (package id contains "reportgenerator" or
// a command is "reportgenerator"). Best-effort: unreadable/garbled → false.
func toolManifestDeclaresReportGenerator(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc struct {
		Tools map[string]struct {
			Commands []string `json:"commands"`
		} `json:"tools"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return false
	}
	for id, tool := range doc.Tools {
		if strings.Contains(strings.ToLower(id), "reportgenerator") {
			return true
		}
		for _, c := range tool.Commands {
			if strings.EqualFold(c, "reportgenerator") {
				return true
			}
		}
	}
	return false
}

// buildReportGeneratorArgs assembles the ReportGenerator argv after the command
// prefix: -reports (semicolon-joined), -targetdir, -reporttypes (default Html),
// and -license when a Pro key is configured. Pure.
func buildReportGeneratorArgs(reports []string, targetDir, reportTypes, license string) []string {
	if reportTypes == "" {
		reportTypes = "Html"
	}
	args := []string{
		"-reports:" + strings.Join(reports, ";"),
		"-targetdir:" + targetDir,
		"-reporttypes:" + reportTypes,
	}
	if strings.TrimSpace(license) != "" {
		args = append(args, "-license:"+license)
	}
	return args
}

// runReportGenerator runs ReportGenerator over reports into a target dir and
// returns the produced HTML index path. Errors propagate so the caller can fall
// back to the native report.
func runReportGenerator(cmd *cobra.Command, root string, invoker, reports []string, targetDir string, cov *config.Coverage) (string, error) {
	reportTypes, license := "", ""
	if cov != nil {
		reportTypes, license = cov.ReportTypes, cov.License
	}
	argv := append(append([]string{}, invoker...), buildReportGeneratorArgs(reports, targetDir, reportTypes, license)...)
	echo(cmd, strings.Join(argv, " "))
	if dryRun {
		return filepath.Join(targetDir, "index.html"), nil
	}
	c := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
	c.Dir = root
	c.Env = commandEnv(root)
	if out, err := c.CombinedOutput(); err != nil {
		return "", fmt.Errorf("reportgenerator: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	for _, name := range []string{"index.html", "index.htm"} {
		if p := filepath.Join(targetDir, name); fileExists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("reportgenerator produced no index.html in %s", targetDir)
}

// fileExists reports whether path is an existing file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// produceCoverageReport renders the coverage report for eco and opens it. It
// prefers ReportGenerator when available and an RG-readable report exists
// (Cobertura for .NET, lcov/Cobertura for node, a converted profile for go),
// and falls back to the native per-ecosystem report otherwise. Best-effort:
// prints a note when nothing can be produced. The go path is handled by the
// caller (runGoCoverage), which already has the coverage profile in hand.
func produceCoverageReport(cmd *cobra.Command, eco, root string, cov *config.Coverage) {
	invoker, rgOK := toolReportGenerator.ensure(cmd, root)
	if rgOK {
		if reports := rgInputsFor(eco, root); len(reports) > 0 {
			target := reportTargetDir(eco, root)
			if index, err := runReportGenerator(cmd, root, invoker, reports, target, cov); err == nil {
				openReportFile(cmd, index)
				return
			} else {
				fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render("reportgenerator failed; using the native report ("+err.Error()+")"))
			}
		}
	}
	nativeCoverageReport(cmd, eco, root)
}

// rgInputsFor returns the ReportGenerator-readable coverage files for eco, or
// nil when none are present. (go is handled by runGoCoverage.)
func rgInputsFor(eco, root string) []string {
	switch eco {
	case detect.DotNet:
		if c := findNewestCobertura(root); c != "" {
			return []string{c}
		}
	case detect.Node:
		if l := filepath.Join(root, "coverage", "lcov.info"); fileExists(l) {
			return []string{l}
		}
		if c := findNewestCobertura(root); c != "" {
			return []string{c}
		}
	}
	return nil
}

// reportTargetDir is where ReportGenerator writes its HTML for eco, preferring a
// per-ecosystem gitignored location; go (and anything else) uses a temp dir.
func reportTargetDir(eco, root string) string {
	switch eco {
	case detect.DotNet:
		return filepath.Join(root, "obj", "rig", "coverage-report")
	case detect.Node:
		return filepath.Join(root, "coverage", "rig-report")
	default:
		if d, err := os.MkdirTemp("", "rig-coverage-"); err == nil {
			return d
		}
		return filepath.Join(root, ".rig-coverage-report")
	}
}

// nativeCoverageReport renders/opens the dependency-free report for eco when
// ReportGenerator isn't used: .NET → in-process Cobertura HTML; node → the
// Istanbul HTML the runner already wrote.
func nativeCoverageReport(cmd *cobra.Command, eco, root string) {
	var report string
	switch eco {
	case detect.Node:
		for _, rel := range []string{
			filepath.Join("coverage", "index.html"),
			filepath.Join("coverage", "lcov-report", "index.html"),
		} {
			if p := filepath.Join(root, rel); fileExists(p) {
				report = p
				break
			}
		}
	case detect.DotNet:
		if cobertura := findNewestCobertura(root); cobertura != "" {
			target := filepath.Join(root, "obj", "rig", "coverage-report")
			if index, err := renderCoberturaHTML(cobertura, target); err == nil {
				report = index
			}
		}
	}
	if report == "" {
		fmt.Fprintln(cmd.OutOrStdout(), dimStyle.Render("no HTML report found to open"))
		return
	}
	openReportFile(cmd, report)
}

// openReportFile opens a produced report and notes it (or the failure).
func openReportFile(cmd *cobra.Command, report string) {
	if err := openPath(cmd, report); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render("could not open report: "+err.Error()))
		return
	}
	fmt.Fprintln(cmd.OutOrStdout(), dimStyle.Render("opened "+report))
}

// produceGoReport renders the go coverage profile: ReportGenerator (via an
// in-process Cobertura conversion) when available, else `go tool cover -html`.
func produceGoReport(cmd *cobra.Command, root, profile string, cov *config.Coverage) {
	if invoker, ok := toolReportGenerator.ensure(cmd, root); ok {
		if cobertura, err := writeGoCobertura(profile); err == nil {
			target := reportTargetDir(detect.Go, root)
			if index, rerr := runReportGenerator(cmd, root, invoker, []string{cobertura}, target, cov); rerr == nil {
				openReportFile(cmd, index)
				return
			} else {
				fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render("reportgenerator failed; using `go tool cover -html` ("+rerr.Error()+")"))
			}
		}
	}
	target := reportTargetDir(detect.Go, root)
	if err := os.MkdirAll(target, 0o755); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render("could not prepare report dir: "+err.Error()))
		return
	}
	index := filepath.Join(target, "index.html")
	c := exec.CommandContext(cmd.Context(), "go", "tool", "cover", "-html="+profile, "-o", index)
	c.Dir = root
	c.Env = commandEnv(root)
	if out, err := c.CombinedOutput(); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render("go tool cover failed: "+strings.TrimSpace(string(out))))
		return
	}
	openReportFile(cmd, index)
}

// writeGoCobertura converts a go coverage profile to a temp Cobertura file for
// ReportGenerator and returns its path.
func writeGoCobertura(profile string) (string, error) {
	data, err := os.ReadFile(profile)
	if err != nil {
		return "", err
	}
	xmlDoc, err := goProfileToCobertura(string(data))
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "rig-go-*.cobertura.xml")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(xmlDoc); err != nil {
		f.Close()
		return "", err
	}
	return f.Name(), f.Close()
}

// goProfileToCobertura converts a `go test -coverprofile` profile to a minimal
// Cobertura document (one class per file, per-line hits) that ReportGenerator
// reads. Statement blocks are flattened to line granularity, taking the max hit
// count per line. Pure — unit-tested.
func goProfileToCobertura(profile string) (string, error) {
	files := map[string]map[int]int{} // file → line → max hits
	var order []string
	sc := bufio.NewScanner(strings.NewReader(profile))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line) // "file:sL.sC,eL.eC" numStmts count
		if len(fields) != 3 {
			continue
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		colon := strings.LastIndex(fields[0], ":")
		if colon < 0 {
			continue
		}
		file, rng := fields[0][:colon], fields[0][colon+1:]
		comma := strings.Index(rng, ",")
		if comma < 0 {
			continue
		}
		start, end := lineNum(rng[:comma]), lineNum(rng[comma+1:])
		if start == 0 {
			continue
		}
		if end < start {
			end = start
		}
		m, ok := files[file]
		if !ok {
			m = map[int]int{}
			files[file] = m
			order = append(order, file)
		}
		for ln := start; ln <= end; ln++ {
			if cur, seen := m[ln]; !seen || count > cur {
				m[ln] = count
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	sort.Strings(order)

	var totalLines, totalCovered int
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	var classes strings.Builder
	for _, file := range order {
		lines := files[file]
		nums := make([]int, 0, len(lines))
		for ln := range lines {
			nums = append(nums, ln)
		}
		sort.Ints(nums)
		covered := 0
		var lb strings.Builder
		for _, ln := range nums {
			hits := lines[ln]
			if hits > 0 {
				covered++
			}
			fmt.Fprintf(&lb, `        <line number="%d" hits="%d"/>`+"\n", ln, hits)
		}
		totalLines += len(nums)
		totalCovered += covered
		fmt.Fprintf(&classes, `      <class name="%s" filename="%s" line-rate="%s" branch-rate="0">`+"\n",
			html.EscapeString(filepath.Base(file)), html.EscapeString(file), ratio(covered, len(nums)))
		classes.WriteString("        <lines>\n")
		classes.WriteString(lb.String())
		classes.WriteString("        </lines>\n      </class>\n")
	}
	fmt.Fprintf(&b, `<coverage line-rate="%s" branch-rate="0" version="rig" timestamp="0">`+"\n", ratio(totalCovered, totalLines))
	b.WriteString("  <packages>\n    <package name=\"go\" line-rate=\"" + ratio(totalCovered, totalLines) + "\" branch-rate=\"0\">\n")
	b.WriteString("      <classes>\n")
	b.WriteString(classes.String())
	b.WriteString("      </classes>\n    </package>\n  </packages>\n</coverage>\n")
	return b.String(), nil
}

// lineNum parses the integer before the first '.' in a "line.col" token.
func lineNum(s string) int {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	n, _ := strconv.Atoi(s)
	return n
}

// ratio formats covered/total as a 0–1 Cobertura rate ("1" when total is 0).
func ratio(covered, total int) string {
	if total == 0 {
		return "1"
	}
	return strconv.FormatFloat(float64(covered)/float64(total), 'f', 4, 64)
}

// augmentNodeCoverageArgs forwards the optional [name] filter and, for vitest,
// the coverage reporters --min/--open/the summary table need — everything past
// a single `--` so the package manager hands them to the test runner (a bare
// positional after `<pm> run coverage` is dropped without it). The reporters
// (lcov → ReportGenerator input, html → native open, json-summary → --min and
// the summary table) are only added for vitest, whose `--coverage.reporter`
// flag repeats cleanly; other
// runners just get the forwarded name and rig consumes whatever they wrote.
func augmentNodeCoverageArgs(argv []string, root, name string, open, min, summary bool, cov *config.Coverage) []string {
	vitest := (open || min || summary) && nodeUsesVitest(root)
	if name == "" && !vitest {
		return argv
	}
	out := append([]string{}, argv...)
	out = append(out, "--") // forward everything past here to the test runner
	if name != "" {
		out = append(out, name)
	}
	if vitest {
		out = append(out, "--coverage",
			"--coverage.reporter=lcov", "--coverage.reporter=html", "--coverage.reporter=json-summary")
	}
	return out
}

// toolReportGenerator is the optional ReportGenerator tool. Unlike the cargo
// tools it resolves via several strategies (a `reportgenerator` on PATH, a local
// dotnet tool-manifest, or — in install mode — fetch-on-use via dnx) and has a
// native fallback, so callers use ensure() (not require()). It keeps its own
// coverage.reportGenerator config key and is only "installable" when dnx is
// present (the fetch vehicle), so the shared prompt is offered exactly when the
// old bespoke flow did.
var toolReportGenerator = extTool{
	name:       "reportgenerator",
	why:        "renders a richer HTML coverage report",
	hint:       "fetched on demand via dnx, or: dotnet tool install -g dotnet-reportgenerator-globaltool",
	resolve:    func(root, mode string) ([]string, bool) { return resolveReportGenerator(root, mode) },
	canInstall: func(string) bool { _, err := exec.LookPath("dnx"); return err == nil },
	readMode: func(cfg config.Config) string {
		if cfg.Coverage != nil {
			return cfg.Coverage.ReportGenerator
		}
		return ""
	},
	configKey: []string{"coverage", "reportGenerator"},
}

// nodeUsesVitest reports whether the repo's node project uses vitest (a vitest
// config file, or a vitest dependency in package.json).
func nodeUsesVitest(root string) bool {
	for _, f := range []string{"vitest.config.ts", "vitest.config.js", "vitest.config.mts", "vitest.config.mjs"} {
		if fileExists(filepath.Join(root, f)) {
			return true
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return false
	}
	var pj struct {
		Deps map[string]string `json:"dependencies"`
		Dev  map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(data, &pj) != nil {
		return false
	}
	_, a := pj.Deps["vitest"]
	_, b := pj.Dev["vitest"]
	return a || b
}
