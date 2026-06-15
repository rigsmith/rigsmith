package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/doctor"
	"github.com/rigsmith/rigsmith/internal/doctorui"
	"github.com/rigsmith/rigsmith/internal/rig/config"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
	"github.com/spf13/cobra"
)

// warnStyle marks warning-level doctor checks (yellow).
var warnStyle = lipgloss.NewStyle().Foreground(brandYellow)

// ecoHeaderStyle is the per-ecosystem group header (bold cyan).
var ecoHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(brandCyan)

// docLevel is a check's severity. Only docError fails the command.
type docLevel int

const (
	docOK docLevel = iota
	docWarn
	docError
)

// newDoctorCmd builds `rig doctor` — environment checks for the detected
// ecosystem(s). It prints a ✓/!/✗ checklist and exits non-zero only when an
// error-level check fails (warnings don't fail), so it doubles as a CI / pre-push
// gate. It mirrors the .NET/Node `rig doctor` verbs. After the checklist it offers
// to install the optional tools rig owns an install command for (`--fix` to
// install them all without prompting); the core toolchains stay report-only.
func newDoctorCmd() *cobra.Command {
	var fixAll bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the environment for the detected ecosystem(s)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)

			fmt.Fprintln(out, headerStyle.Render("rig doctor")+"  "+dimStyle.Render(root))
			fmt.Fprintln(out)

			checks, present := gatherChecks(cmd, root)
			if len(checks) == 0 {
				fmt.Fprintln(out, dimStyle.Render(
					"no recognized projects (.NET/Node/Go/Cargo) found here — nothing to check"))
				return nil
			}

			var severity docLevel
			if doctorLiveEligible() {
				// Live checklist: each check spins until its probe resolves.
				severity = runDoctorLive(cmd, checks)
			} else {
				// Static path (CI / piped / --quiet): run and print each line,
				// grouped under an ecosystem header.
				lastEco := ""
				for _, pc := range checks {
					if pc.eco != lastEco {
						fmt.Fprintln(out, ecoHeaderStyle.Render(ecoDisplayName(pc.eco)))
						lastEco = pc.eco
					}
					c := pc.run()
					if c.level > severity {
						severity = c.level
					}
					fmt.Fprintln(out, docRowLine(renderMark(c.level), c.label, c.detail, pc.path))
				}
				fmt.Fprintln(out)
				fmt.Fprintln(out, doctorSummary(severity))
			}

			// Offer to install the missing optional tools rig can install (owned
			// tools only — toolchains stay report-only, with the .NET page below).
			if fixes := doctorToolFixes(cmd, present, root); len(fixes) > 0 {
				doctorui.RunFixes(cmd, fixes, doctorui.Options{
					Accent:      brand.AccentRig,
					FixAll:      fixAll,
					Interactive: !quiet && interactive(),
				})
			}

			// On a TTY with a .NET project here but no SDK, offer the install page.
			maybeOfferDotnetInstall(cmd, root)

			if severity == docError {
				return fmt.Errorf("doctor: problems found")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fixAll, "fix", false, "install every missing tool rig can install, without prompting")
	return cmd
}

// dotnetTargetMajor is the .NET SDK major version rig standardizes on. Older
// SDKs still work for most verbs (a graceful warn in doctor), but the dnx-based
// features (the `dlx` verb, on-demand ReportGenerator) need this one.
const dotnetTargetMajor = 10

// maybeOfferDotnetInstall offers to open the .NET download page when a .NET
// project is here but no SDK is installed, on an interactive terminal. (The
// checklist already reported the error; this is the actionable follow-up.)
func maybeOfferDotnetInstall(cmd *cobra.Command, root string) {
	if !doctorLiveEligible() {
		return
	}
	if _, err := exec.LookPath("dotnet"); err == nil {
		return
	}
	if len(discoverDotnetProjects(root)) == 0 {
		return
	}
	offerOpenURL(cmd, "the .NET SDK isn't installed", "https://dot.net/download")
}

// check is one checklist line.
type check struct {
	level  docLevel
	label  string
	detail string
}

func ok(label, detail string) check   { return check{docOK, label, detail} }
func warn(label, detail string) check { return check{docWarn, label, detail} }
func bad(label, detail string) check  { return check{docError, label, detail} }

// pendingCheck is a check whose label is known up front but whose result comes
// from running it (a probe that may shell out). The live checklist shows the
// label with a spinner, then fills in the result; the static path just runs it.
// eco is the owning ecosystem id, used to group the output.
type pendingCheck struct {
	eco   string
	label string
	path  string // repo-relative project dir for per-project rows ("" for toolchain rows)
	run   func() check
}

// docRowLine formats one checklist line: mark, padded label, dim detail, and —
// for per-project rows — the repo-relative path in a trailing dim column.
// Shared by the static and live renderers so they stay identical.
func docRowLine(glyph, label, detail, path string) string {
	body := detail
	if path != "" {
		body = pad(detail+"  ", 24) + path // align the path into a column after the detail
	}
	return "  " + glyph + " " + pad(label, 10) + " " + dimStyle.Render(body)
}

// gatherChecks builds the ordered, ecosystem-grouped check list for the whole
// workspace: it discovers every project (the shared workspace searcher), and for
// each ecosystem emits the toolchain rows once, then a row per project with its
// own state (node deps / .NET TFM / go+cargo versions).
func gatherChecks(cmd *cobra.Command, root string) ([]pendingCheck, map[string]bool) {
	targets := discoverWorkspace(cdContext(cmd), root, excludeFor(root))
	byEco := map[string][]target{}
	for _, t := range targets {
		// .NET is handled by the presence scan below: discoverWorkspace is
		// release-discovery and skips version-less projects (most apps), but
		// doctor is an environment check and should list them too.
		if t.Eco == detect.DotNet {
			continue
		}
		byEco[t.Eco] = append(byEco[t.Eco], t)
	}
	if dn := discoverDotnetProjects(root); len(dn) > 0 {
		byEco[detect.DotNet] = dn
	}

	// The ecosystems present, and every discovered project name (full + short)
	// — used by the tools and config groups below.
	present := map[string]bool{}
	names := map[string]bool{}
	for eco, ts := range byEco {
		if len(ts) > 0 {
			present[eco] = true
		}
		for _, t := range ts {
			names[t.Name] = true
			names[shortName(t.Name)] = true
		}
	}

	var all []pendingCheck
	for _, eco := range orderedEcos(byEco) {
		for _, pc := range toolchainChecks(cmd, eco, root) {
			pc.eco = eco
			all = append(all, pc)
		}
		pkgs := byEco[eco]
		sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
		for _, t := range pkgs {
			pc := packageCheck(eco, t, root)
			pc.eco = eco
			all = append(all, pc)
		}
	}
	// Optional external tools, then config-path validation — each its own group,
	// emitted only when it has rows.
	all = append(all, toolChecks(present, root)...)
	all = append(all, configChecks(root, names)...)
	return all, present
}

// relevantTools is the set of optional external tools (extTool values) that
// matter for the present ecosystems, deduped (ReportGenerator is shared by
// .NET/Node/Go coverage). Shared by toolChecks (the report rows) and
// doctorToolFixes (the install offer) so they never drift.
func relevantTools(present map[string]bool, root string) []extTool {
	var tools []extTool
	rgAdded := false
	addRG := func() {
		if !rgAdded {
			tools = append(tools, toolReportGenerator)
			rgAdded = true
		}
	}
	if present[detect.Cargo] {
		tools = append(tools, toolCargoLlvmCov, toolCargoOutdated, toolCargoWatch)
	}
	if present[detect.DotNet] {
		tools = append(tools, toolDnx)
		if dotnetFormatterIsCsharpier(root) {
			tools = append(tools, toolCsharpier)
		}
		addRG()
	}
	if present[detect.Node] {
		addRG()
	}
	if present[detect.Go] {
		tools = append(tools, toolWgo)
		addRG()
	}
	return tools
}

// toolChecks reports the optional external tools relevant to the present
// ecosystems and whether each is installed. A missing optional tool is a warn —
// it only bites when you use the feature it powers.
func toolChecks(present map[string]bool, root string) []pendingCheck {
	var out []pendingCheck
	for _, t := range relevantTools(present, root) {
		t := t // capture
		out = append(out, pendingCheck{eco: "tools", label: t.name, run: func() check {
			if t.available(root) {
				return ok(t.name, "installed")
			}
			return warn(t.name, "not installed — "+toolHowto(t))
		}})
	}
	return out
}

// doctorToolFixes builds the install offer for the optional tools that are
// missing AND rig owns an install command for — the "owned tools only" line:
// only tools with a real `install` command appear (so fetch-on-use ReportGenerator
// and SDK-shipped dnx don't), and a tool the user pinned `off` stays report-only.
// A tool whose installer itself is missing (e.g. `cargo install …` with no cargo)
// is skipped too — the install couldn't succeed, and the toolchain's own row
// already flags that. Each result carries a Fix that runs the install; doctorui
// presents and applies.
func doctorToolFixes(cmd *cobra.Command, present map[string]bool, root string) []doctor.Section {
	var results []doctor.Result
	for _, t := range relevantTools(present, root) {
		t := t // capture
		if t.available(root) || len(t.install) == 0 || t.mode(root) == toolOff || !installerOnPath(t) {
			continue
		}
		results = append(results, doctor.Result{
			Name:     t.name,
			Status:   doctor.Warn,
			Detail:   "not installed — " + t.why,
			Fix:      func(context.Context) error { return t.installNow(cmd, root) },
			FixLabel: "install " + t.name + "  (" + strings.Join(t.install, " ") + ")",
		})
	}
	if len(results) == 0 {
		return nil
	}
	return []doctor.Section{{Title: "tools", Results: results}}
}

// installerOnPath reports whether the command that installs t (the first word of
// its install command, e.g. `cargo` / `go` / `dotnet`) is itself resolvable —
// the precondition for the install to have any chance of succeeding.
func installerOnPath(t extTool) bool {
	if len(t.install) == 0 {
		return false
	}
	_, err := exec.LookPath(t.install[0])
	return err == nil
}

// toolHowto is the "how to get it" suffix for a missing tool: its install
// command, else its hint, else what it's for.
func toolHowto(t extTool) string {
	switch {
	case len(t.install) > 0:
		return strings.Join(t.install, " ")
	case t.hint != "":
		return t.hint
	default:
		return t.why
	}
}

// configChecks validates that the paths a merged .rig.json points at resolve: a
// pinned `solution` file, a `defaultProject` that names a real project, and any
// custom-command `cwd`. Rows are emitted only for keys that are set, so a repo
// without these shows no Config group. A broken path is an error (it will fail a
// real command).
func configChecks(root string, projectNames map[string]bool) []pendingCheck {
	cfg, err := config.LoadMerged(root)
	if err != nil {
		return nil
	}
	var out []pendingCheck
	if sol := strings.TrimSpace(cfg.Solution); sol != "" {
		out = append(out, pendingCheck{eco: "config", label: "solution", run: func() check {
			if exists(filepath.Join(root, sol)) {
				return ok("solution", sol)
			}
			return bad("solution", sol+" — not found (config: solution)")
		}})
	}
	if dp := strings.TrimSpace(cfg.DefaultProject); dp != "" {
		out = append(out, pendingCheck{eco: "config", label: "default", run: func() check {
			if projectNames[dp] || projectNames[shortName(dp)] {
				return ok("default", dp)
			}
			return bad("default", dp+" — matches no discovered project (config: defaultProject)")
		}})
	}
	cmdNames := make([]string, 0, len(cfg.Commands))
	for name := range cfg.Commands {
		cmdNames = append(cmdNames, name)
	}
	sort.Strings(cmdNames)
	for _, name := range cmdNames {
		c := cfg.Commands[name]
		if c == nil || strings.TrimSpace(c.Cwd) == "" {
			continue
		}
		cwd := c.Cwd
		label := shortName(name)
		out = append(out, pendingCheck{eco: "config", label: label, run: func() check {
			if info, serr := os.Stat(filepath.Join(root, cwd)); serr == nil && info.IsDir() {
				return ok(label, "cwd "+cwd)
			}
			return bad(label, cwd+" — cwd not found (config: commands."+name+".cwd)")
		}})
	}
	return out
}

// orderedEcos returns the ecosystems present, in a stable canonical order.
func orderedEcos(byEco map[string][]target) []string {
	var out []string
	for _, eco := range []string{detect.Go, detect.Node, detect.DotNet, detect.Cargo} {
		if len(byEco[eco]) > 0 {
			out = append(out, eco)
		}
	}
	for eco := range byEco {
		switch eco {
		case detect.Go, detect.Node, detect.DotNet, detect.Cargo:
		default:
			out = append(out, eco)
		}
	}
	return out
}

// ecoDisplayName is the human header for an ecosystem group.
func ecoDisplayName(eco string) string {
	switch eco {
	case detect.Go:
		return "Go"
	case detect.Node:
		return "Node"
	case detect.DotNet:
		return ".NET"
	case detect.Cargo:
		return "Cargo"
	case "tools":
		return "Tools"
	case "config":
		return "Config"
	}
	return eco
}

// toolchainChecks are the once-per-ecosystem environment probes (is the tool
// installed, which version) that head each group.
func toolchainChecks(cmd *cobra.Command, eco, root string) []pendingCheck {
	switch eco {
	case detect.Go:
		return []pendingCheck{{label: "go", run: func() check {
			if v, present := probeVersion(cmd, root, "go", "version"); present {
				return ok2("go", strings.TrimPrefix(v, "go version "))
			}
			return bad("go", "the `go` command isn't on your PATH — install Go")
		}}}
	case detect.Node:
		pm := string(detect.DetectNodePM(root))
		return []pendingCheck{
			{label: "node", run: func() check {
				if v, present := probeVersion(cmd, root, "node", "--version"); present {
					return ok2("node", v)
				}
				return bad("node", "the `node` runtime isn't on your PATH — install Node.js")
			}},
			{label: pm, run: func() check {
				if v, present := probeVersion(cmd, root, pm, "--version"); present {
					return ok2(pm, pm+" "+v)
				}
				return bad(pm, fmt.Sprintf("package manager %q isn't on your PATH (detected from the lockfile)", pm))
			}},
		}
	case detect.DotNet:
		return []pendingCheck{{label: "dotnet", run: func() check {
			sdk, present := probeVersion(cmd, root, "dotnet", "--version")
			if !present {
				return bad("dotnet", fmt.Sprintf(
					"no .NET SDK on your PATH — install .NET %d from https://dot.net/download", dotnetTargetMajor))
			}
			// A global.json pin the installed SDK can't satisfy is a hard error.
			pin := readSdkPin(root)
			if pin != "" && !sdkSatisfies(sdk, pin) {
				return bad("dotnet", fmt.Sprintf("SDK %s is installed, but global.json pins %s — install that SDK", sdk, pin))
			}
			detail := "SDK " + sdk
			if pin != "" {
				detail += fmt.Sprintf(" (satisfies global.json pin %s)", pin)
			}
			// rig standardizes on .NET 10; an older SDK still works for most
			// verbs but not the dnx-based features, so it's a graceful warn.
			if major, ok := majorOf(sdk); ok && major < dotnetTargetMajor {
				return warn("dotnet", detail+fmt.Sprintf(
					" — rig targets .NET %d; dnx-based features (dlx, on-demand ReportGenerator) need %d",
					dotnetTargetMajor, dotnetTargetMajor))
			}
			return ok2("dotnet", detail)
		}}, {label: "analyzers", run: func() check {
			// `rig lint` runs `dotnet format analyzers`; without a third-party
			// analyzer package it only exercises the SDK's built-in rules, so flag
			// the absence as the it-only-bites-when-you-lint warn.
			pkgs := dotnetAnalyzerPackages(root)
			if len(pkgs) == 0 {
				return warn("analyzers", "none referenced — `rig lint` runs only the SDK's built-in rules; "+
					"add SonarAnalyzer.CSharp or Meziantou.Analyzer for deeper analysis")
			}
			return ok2("analyzers", strings.Join(pkgs, ", ")+" — `rig lint` runs these")
		}}}
	case detect.Cargo:
		return []pendingCheck{{label: "cargo", run: func() check {
			if v, present := probeVersion(cmd, root, "cargo", "--version"); present {
				return ok2("cargo", strings.TrimPrefix(v, "cargo "))
			}
			return bad("cargo", "the `cargo` command isn't on your PATH — install Rust (rustup)")
		}}}
	}
	return nil
}

// packageCheck builds the per-project row for one discovered package, labeled by
// its short name with an ecosystem-appropriate detail.
func packageCheck(eco string, t target, root string) pendingCheck {
	name := shortName(t.Name)
	dir := t.Dir
	rel := filepath.ToSlash(relToRoot(root, dir))
	switch eco {
	case detect.Node:
		return pendingCheck{label: name, path: rel, run: func() check {
			switch {
			case !nodeHasDependencies(dir):
				return ok(name, "no dependencies")
			case exists(filepath.Join(dir, "node_modules")) || exists(filepath.Join(root, "node_modules")):
				return ok(name, "deps installed")
			default:
				return warn(name, "deps declared, not installed — run `rig install`")
			}
		}}
	case detect.DotNet:
		return pendingCheck{label: name, path: rel, run: func() check {
			if tfm := readTargetFramework(dir); tfm != "" {
				return ok(name, tfm)
			}
			return ok(name, "project")
		}}
	case detect.Go:
		return pendingCheck{label: name, path: rel, run: func() check {
			if v := readGoVersion(dir); v != "" {
				return ok(name, "go "+v)
			}
			return ok(name, "module")
		}}
	case detect.Cargo:
		return pendingCheck{label: name, path: rel, run: func() check {
			if v := readCargoVersion(dir); v != "" {
				return ok(name, "v"+v)
			}
			return ok(name, "crate")
		}}
	}
	return pendingCheck{label: name, run: func() check { return ok(name, "") }}
}

var (
	tfmRe      = regexp.MustCompile(`(?i)<TargetFrameworks?>([^<]+)</TargetFrameworks?>`)
	goVerRe    = regexp.MustCompile(`(?m)^go\s+(\S+)`)
	cargoVerRe = regexp.MustCompile(`(?m)^\s*version\s*=\s*"([^"]+)"`)
)

// dotnetProjectGlobs are the project-file patterns a .NET project is found by.
var dotnetProjectGlobs = []string{"*.csproj", "*.fsproj", "*.vbproj"}

// discoverDotnetProjects finds every .NET project by presence (walking for
// project files), independent of whether it declares a version — doctor lists
// apps (usually version-less) alongside versioned libraries. bin/obj/.git/
// node_modules are skipped; the `exclude` globs are honored.
func discoverDotnetProjects(root string) []target {
	exclude := excludeFor(root)
	skip := map[string]bool{"bin": true, "obj": true, ".git": true, "node_modules": true, "vendor": true}
	var out []target
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".csproj", ".fsproj", ".vbproj":
			name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			if !excluded(name, exclude) {
				out = append(out, target{Name: name, Eco: detect.DotNet, Dir: filepath.Dir(path)})
			}
		}
		return nil
	})
	return out
}

// readTargetFramework returns a project's TargetFramework(s): inline in the
// project file first, then from an ancestor Directory.Build.props (the common
// place to set it repo-wide).
func readTargetFramework(dir string) string {
	for _, pat := range dotnetProjectGlobs {
		ms, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, m := range ms {
			if tfm := tfmFromFile(m); tfm != "" {
				return tfm
			}
		}
	}
	for d := dir; ; {
		if tfm := tfmFromFile(filepath.Join(d, "Directory.Build.props")); tfm != "" {
			return tfm
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return ""
}

// tfmFromFile reads a single TargetFramework(s) value out of an MSBuild file.
func tfmFromFile(path string) string {
	if data, err := os.ReadFile(path); err == nil {
		if m := tfmRe.FindSubmatch(data); m != nil {
			return strings.TrimSpace(string(m[1]))
		}
	}
	return ""
}

// readGoVersion returns the go directive version from dir/go.mod.
func readGoVersion(dir string) string {
	if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
		if m := goVerRe.FindSubmatch(data); m != nil {
			return string(m[1])
		}
	}
	return ""
}

// readCargoVersion returns the package version from dir/Cargo.toml (best-effort:
// the first `version = "..."`, which is the [package] one in a normal manifest).
func readCargoVersion(dir string) string {
	if data, err := os.ReadFile(filepath.Join(dir, "Cargo.toml")); err == nil {
		if m := cargoVerRe.FindSubmatch(data); m != nil {
			return string(m[1])
		}
	}
	return ""
}

// nodeHasDependencies reports whether dir/package.json declares any
// (dev)dependencies — used to keep the "deps not installed" warning quiet for a
// project that has nothing to install.
func nodeHasDependencies(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
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
	return len(pj.Deps) > 0 || len(pj.Dev) > 0
}

// ok2 is ok() with the detail already trimmed — a small helper so version
// strings render cleanly.
func ok2(label, detail string) check { return ok(label, strings.TrimSpace(detail)) }

// probeVersion runs `bin args...` and returns its trimmed first line of output.
// ok=false means the binary isn't on PATH or exited non-zero.
func probeVersion(cmd *cobra.Command, root, bin string, args ...string) (string, bool) {
	c := exec.CommandContext(cmd.Context(), bin, args...)
	c.Dir = root
	b, err := c.Output()
	if err != nil {
		return "", false
	}
	out := strings.TrimSpace(string(b))
	if out == "" {
		return "", false
	}
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = strings.TrimSpace(out[:i])
	}
	return out, true
}

// readSdkPin returns the sdk.version pinned by the nearest global.json at or
// above root, or "" when none pins one. Tolerant: a missing/garbled file is
// treated as no pin, and the nearest global.json wins, pin or not (a parent's
// pin never overrides a closer, pin-less file).
func readSdkPin(root string) string {
	for dir := root; ; {
		data, err := os.ReadFile(filepath.Join(dir, "global.json"))
		if err == nil {
			var doc struct {
				SDK struct {
					Version string `json:"version"`
				} `json:"sdk"`
			}
			if json.Unmarshal(data, &doc) != nil {
				return "" // unreadable global.json → treat as no pin
			}
			return doc.SDK.Version
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// sdkSatisfies reports whether an installed SDK version satisfies a global.json
// pin. Heuristic: same-or-newer major is fine (rollForward usually covers the
// rest); an unparseable side defers to satisfied. Pure — unit-tested.
func sdkSatisfies(installed, pinned string) bool {
	if strings.TrimSpace(pinned) == "" {
		return true
	}
	have, okH := majorOf(installed)
	need, okN := majorOf(pinned)
	if !okH || !okN {
		return true
	}
	return have >= need
}

// majorOf parses the leading major version number out of a version string.
func majorOf(version string) (int, bool) {
	version = strings.TrimSpace(version)
	head := version
	if i := strings.IndexByte(version, '.'); i >= 0 {
		head = version[:i]
	}
	n, err := strconv.Atoi(head)
	if err != nil {
		return 0, false
	}
	return n, true
}

func hasDotNet(root string) bool {
	return hasSolution(root) || hasCsproj(root)
}

func hasSolution(root string) bool {
	for _, pat := range []string{"*.sln", "*.slnx"} {
		if m, _ := filepath.Glob(filepath.Join(root, pat)); len(m) > 0 {
			return true
		}
	}
	return false
}

func hasCsproj(root string) bool {
	for _, pat := range []string{"*.csproj", "*.fsproj", "*.vbproj"} {
		if m, _ := filepath.Glob(filepath.Join(root, pat)); len(m) > 0 {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// doctorSummary renders the final one-line verdict for a severity.
func doctorSummary(severity docLevel) string {
	switch severity {
	case docOK:
		return okStyle.Render("all good")
	case docWarn:
		return warnStyle.Render("some warnings")
	default:
		return failStyle.Render("problems found")
	}
}

// renderMark returns the colored status glyph for a level.
func renderMark(level docLevel) string {
	switch level {
	case docOK:
		return okStyle.Render("✓")
	case docWarn:
		return warnStyle.Render("!")
	default:
		return failStyle.Render("✗")
	}
}

// pad right-pads s to width (for column alignment).
func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
