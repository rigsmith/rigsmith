package cli

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// Styles for coverage/doctor result lines. Kept local to avoid touching the
// shared root.go; dimStyle there is reused for the muted details.
var (
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	failStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
)

// newCoverageCmd builds `rig coverage [name]` — run the ecosystem's coverage
// command, then optionally open the HTML report and/or gate on a minimum line
// coverage. The report prefers ReportGenerator when it's available (it reads
// Cobertura/lcov and renders rich HTML for .NET, node, and — via a profile
// conversion — go); otherwise rig renders a dependency-free native report. See
// coverage_report.go. The ecosystem's coverage command is supplied by the
// shared registry (detect.CommandFor):
//
//   - dotnet → dotnet test --collect:"XPlat Code Coverage"
//   - node   → <pm> run coverage
//   - go     → go test -cover ./...
//   - cargo  → (unsupported)
//
// --min <pct> gates line coverage (non-zero exit if below); --open opens the
// produced HTML report when one can be located.
func newCoverageCmd() *cobra.Command {
	var (
		open bool
		min  float64
	)

	cmd := &cobra.Command{
		Use:   "coverage [name]",
		Short: "Run the tests with coverage",
		Long: "Run the detected ecosystem's coverage command.\n\n" +
			"  rig coverage              run coverage for the repo\n" +
			"  rig coverage <name>       narrow to a project/filter (node/.NET)\n" +
			"  rig coverage --open       open the produced HTML report\n" +
			"  rig coverage --min 80     fail if line coverage is below 80%",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			root := detect.Root(cwd)

			eco, err := resolveCoverageEcosystem(cwd, root)
			if err != nil {
				return err
			}

			argv, ok := detect.CommandFor(eco, "coverage", root)
			if !ok {
				return fmt.Errorf("coverage not supported for ecosystem %q", eco)
			}

			var name string
			if len(args) == 1 {
				name = strings.TrimSpace(args[0])
			}
			// Append the optional [name] only where it's meaningful. For Go the
			// `go test -cover ./...` form takes no test name here, so we ignore it.
			if name != "" && eco != detect.Go {
				argv = append(argv, name)
			}

			// Fold the .rig.json `coverage` defaults into the CLI flags: a
			// passed flag always wins, config supplies the default otherwise.
			cfg, _ := config.LoadMerged(root)
			var cliMin *float64
			if cmd.Flags().Changed("min") {
				cliMin = &min
			}
			var effMin *float64
			_, open, effMin = resolveCoverageOptions(false, open, cliMin, cfg.Coverage)

			cov := cfg.Coverage
			// Go is handled end-to-end: it needs the command's stdout for the
			// --min percent and a coverage profile for the report.
			if eco == detect.Go {
				return runGoCoverage(cmd, root, argv, effMin, open, cov)
			}
			// Node: ensure the run emits the reporters --min/--open need.
			if eco == detect.Node {
				argv = augmentNodeCoverageArgs(argv, root, open, effMin != nil, cov)
			}

			if err := runCommand(cmd, root, argv); err != nil {
				return err
			}
			if dryRun {
				return nil
			}

			if effMin != nil {
				if err := gateMinimum(cmd, eco, root, *effMin); err != nil {
					if open { // still produce the report, then surface the gate failure
						produceCoverageReport(cmd, eco, root, cov)
					}
					return err
				}
			}
			if open {
				produceCoverageReport(cmd, eco, root, cov)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&open, "open", false, "open the produced HTML report")
	cmd.Flags().Float64Var(&min, "min", 0, "fail if line coverage is below this percent")
	return cmd
}

// resolveCoverageEcosystem mirrors resolvePrimary's nearest-manifest resolution
// so coverage agrees with the dev verbs about "what kind of repo is this".
func resolveCoverageEcosystem(cwd, root string) (string, error) {
	return resolvePrimary(cwd, root)
}

// gateMinimum reads the produced coverage for node/.NET and fails when line
// coverage is below min. (Go is handled inline where we capture stdout.)
func gateMinimum(cmd *cobra.Command, eco, root string, min float64) error {
	switch eco {
	case detect.Node:
		pct, ok := readNodeLinePct(root)
		if !ok {
			fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render(
				"--min needs a json-summary reporter (coverage/coverage-summary.json not found)"))
			return nil
		}
		return reportMin(cmd, pct, min)
	case detect.DotNet:
		cobertura := findNewestCobertura(root)
		if cobertura == "" {
			fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render(
				"--min: no Cobertura report found under "+root))
			return nil
		}
		line, _ := readCoberturaRates(cobertura)
		if line == nil {
			fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render(
				"--min: could not read line coverage from "+cobertura))
			return nil
		}
		return reportMin(cmd, *line*100, min)
	default:
		return nil
	}
}

// reportMin prints the line coverage and returns an error when it's below min.
func reportMin(cmd *cobra.Command, pct, min float64) error {
	fmt.Fprintf(cmd.OutOrStdout(), "line coverage: %.2f%%\n", pct)
	if pct < min {
		msg := fmt.Sprintf("coverage %.2f%% is below the --min %s%% threshold", pct, trimFloat(min))
		fmt.Fprintln(cmd.ErrOrStderr(), failStyle.Render(msg))
		return fmt.Errorf("%s", msg)
	}
	fmt.Fprintln(cmd.OutOrStdout(), okStyle.Render(
		fmt.Sprintf("line coverage %.2f%% meets the %s%% minimum", pct, trimFloat(min))))
	return nil
}

// readNodeLinePct reads coverage/coverage-summary.json's .total.lines.pct.
func readNodeLinePct(root string) (float64, bool) {
	path := filepath.Join(root, "coverage", "coverage-summary.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	return parseLinePct(data)
}

// parseLinePct extracts .total.lines.pct from a coverage-summary.json blob.
// Pure — unit-tested.
func parseLinePct(summaryJSON []byte) (float64, bool) {
	var data struct {
		Total struct {
			Lines struct {
				Pct *float64 `json:"pct"`
			} `json:"lines"`
		} `json:"total"`
	}
	if err := json.Unmarshal(summaryJSON, &data); err != nil {
		return 0, false
	}
	if data.Total.Lines.Pct == nil {
		return 0, false
	}
	return *data.Total.Lines.Pct, true
}

// goCoverageRe matches Go's "coverage: NN.N% of statements" summary line.
var goCoverageRe = regexp.MustCompile(`coverage:\s*([0-9]+(?:\.[0-9]+)?)%\s+of statements`)

// parseGoCoverage returns the highest "coverage: NN.N% of statements" percent in
// `go test -cover` output (Go prints one per package; we take the max as a
// best-effort overall figure). Pure — unit-tested.
func parseGoCoverage(output string) (float64, bool) {
	matches := goCoverageRe.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return 0, false
	}
	best := -1.0
	for _, m := range matches {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil && v > best {
			best = v
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

// runGoCoverage runs `go test -cover …` end-to-end: it captures stdout so the
// --min percent can be parsed while still streaming output, and when --open is
// set it adds a -coverprofile so a report can be produced (ReportGenerator via
// a Cobertura conversion when available, else `go tool cover -html`).
func runGoCoverage(cmd *cobra.Command, root string, argv []string, min *float64, open bool, cov *config.Coverage) error {
	profile := ""
	if open {
		profile = filepath.Join(root, "coverage.out")
		argv = withGoCoverProfile(argv, profile)
	}
	echo(cmd, strings.Join(argv, " "))
	if dryRun {
		return nil
	}
	c := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
	c.Dir = root
	c.Env = commandEnv(root)
	c.Stderr = cmd.ErrOrStderr() // stderr streams through; Output() captures stdout
	c.Stdin = os.Stdin

	captured, err := c.Output()
	if len(captured) > 0 {
		fmt.Fprint(cmd.OutOrStdout(), string(captured)) // echo so the user still sees output
	}
	if err != nil {
		return err
	}

	if open && profile != "" && fileExists(profile) {
		produceGoReport(cmd, root, profile, cov)
	}
	if min != nil {
		pct, ok := parseGoCoverage(string(captured))
		if !ok {
			fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render(
				"--min: could not read coverage from `go test -cover` output"))
			return nil
		}
		return reportMin(cmd, pct, *min)
	}
	return nil
}

// withGoCoverProfile inserts -coverprofile (+ atomic mode) right after the
// `test` token so the run produces a profile for the report. Pure.
func withGoCoverProfile(argv []string, profile string) []string {
	out := make([]string, 0, len(argv)+2)
	inserted := false
	for _, a := range argv {
		out = append(out, a)
		if !inserted && a == "test" {
			out = append(out, "-coverprofile="+profile, "-covermode=atomic")
			inserted = true
		}
	}
	if !inserted {
		out = append(out, "-coverprofile="+profile, "-covermode=atomic")
	}
	return out
}

// openPath opens path with the platform's default opener.
func openPath(cmd *cobra.Command, path string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "cmd", []string{"/c", "start", ""}
	default:
		name = "xdg-open"
	}
	args = append(args, path)
	c := exec.CommandContext(cmd.Context(), name, args...)
	return c.Start()
}

// trimFloat renders a float without trailing zeros (80.0 → "80", 79.5 → "79.5").
func trimFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	return s
}

// meetsMinimum reports whether a line rate (0–1) meets the minimum percent
// gate, or no minimum is set. An unreadable (nil) rate can't meet a gate.
// Mirrors the .NET rig's CoverageVerb.MeetsMinimum. Pure.
func meetsMinimum(lineRate, minPercent *float64) bool {
	if minPercent == nil {
		return true
	}
	return lineRate != nil && *lineRate*100 >= *minPercent
}

// resolveCoverageOptions folds the .rig.json `coverage` defaults into the CLI
// flags: a passed flag always wins, config supplies the default otherwise
// (bool flags only add). Mirrors the .NET rig's CoverageVerb.ResolveOptions.
// Pure.
func resolveCoverageOptions(cliFull, cliOpen bool, cliMin *float64, cfg *config.Coverage) (full, open bool, min *float64) {
	full = cliFull || (cfg != nil && cfg.Full != nil && *cfg.Full)
	open = cliOpen || (cfg != nil && cfg.Open != nil && *cfg.Open)
	min = cliMin
	if min == nil && cfg != nil {
		min = cfg.Min
	}
	return full, open, min
}

// buildCollectArgs is the `dotnet test …` argument list that collects
// coverage, following the runner's CLI grammar (same as `rig test`): classic
// VSTest takes the project positionally and the XPlat collector; MTP takes
// `--project` and requests coverage after the `--` boundary. Both produce
// Cobertura. Mirrors the .NET rig's CoverageVerb.BuildCollectArgs. Pure.
func buildCollectArgs(runner dotnetTestRunner, testProjectPath, resultsDir, settings, filter string) []string {
	args := []string{"test"}
	if testProjectPath != "" {
		if runner == mtpRunner {
			args = append(args, "--project")
		}
		args = append(args, testProjectPath)
	}
	if filter != "" {
		args = append(args, "--filter", filter)
	}

	if runner == mtpRunner {
		args = append(args, "--", "--coverage", "--coverage-output-format", "cobertura")
		if settings != "" {
			args = append(args, "--coverage-settings", settings)
		}
	} else {
		args = append(args, `--collect:"XPlat Code Coverage"`, "--results-directory", resultsDir)
		if settings != "" {
			args = append(args, "--settings", settings)
		}
	}
	return args
}

// findRunsettings locates the single *.runsettings in the test-project dir,
// else the root. Returns "" when absent or ambiguous (multiple in a dir →
// require explicit `coverage.settings` config rather than guessing). Mirrors
// the .NET rig's CoverageVerb.FindRunsettings.
func findRunsettings(testProjectDir, root string) string {
	seen := make(map[string]bool, 2)
	for _, dir := range []string{testProjectDir, root} {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		hits, _ := filepath.Glob(filepath.Join(dir, "*.runsettings"))
		if len(hits) == 1 {
			return hits[0]
		}
		if len(hits) > 1 {
			return "" // ambiguous → require explicit config
		}
	}
	return ""
}

// readCoberturaRates reads the overall line/branch rates (0–1) from a
// Cobertura root element, nil when missing/unreadable. Mirrors the .NET rig's
// CoverageVerb.ReadRates.
func readCoberturaRates(path string) (line, branch *float64) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var root struct {
		LineRate   string `xml:"line-rate,attr"`
		BranchRate string `xml:"branch-rate,attr"`
	}
	if xml.Unmarshal(data, &root) != nil {
		return nil, nil
	}
	return parseRate(root.LineRate), parseRate(root.BranchRate)
}

// parseRate parses a Cobertura rate attribute, nil when absent/garbled.
func parseRate(s string) *float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return nil
	}
	return &v
}

// findNewestCobertura locates the most recently written *.cobertura.xml under
// root (the XPlat collector writes TestResults/<guid>/coverage.cobertura.xml
// next to the test project). Heavy/irrelevant trees are skipped. "" when none.
func findNewestCobertura(root string) string {
	newest := ""
	var newestTime time.Time
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree — best-effort
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".cobertura.xml") {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil && info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = path
		}
		return nil
	})
	return newest
}

// coberturaDoc is the subset of a Cobertura report rig reads: overall rates,
// the source roots, and per-class per-line hits.
type coberturaDoc struct {
	LineRate   string   `xml:"line-rate,attr"`
	BranchRate string   `xml:"branch-rate,attr"`
	Sources    []string `xml:"sources>source"`
	Packages   []struct {
		Name    string `xml:"name,attr"`
		Classes []struct {
			Name     string `xml:"name,attr"`
			Filename string `xml:"filename,attr"`
			LineRate string `xml:"line-rate,attr"`
			Lines    []struct {
				Number int `xml:"number,attr"`
				Hits   int `xml:"hits,attr"`
			} `xml:"lines>line"`
		} `xml:"classes>class"`
	} `xml:"packages>package"`
}

// renderCoberturaHTML renders a Cobertura report to a standalone HTML page in
// targetDir and returns the index path. This is the dependency-free fallback
// for when ReportGenerator isn't available: an overall summary, a per-file
// table, and — where the source can be resolved from the report's <sources> —
// the source itself with covered/uncovered lines highlighted.
func renderCoberturaHTML(coberturaPath, targetDir string) (string, error) {
	data, err := os.ReadFile(coberturaPath)
	if err != nil {
		return "", err
	}
	var doc coberturaDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return "", err
	}

	pct := func(attr string) string {
		if r := parseRate(attr); r != nil {
			return strconv.FormatFloat(*r*100, 'f', 1, 64) + "%"
		}
		return "n/a"
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html>\n<head><meta charset=\"utf-8\"><title>Coverage</title>\n")
	b.WriteString("<style>body{font-family:system-ui,sans-serif;margin:1.5rem}" +
		"table{border-collapse:collapse}td,th{border:1px solid #ccc;padding:2px 8px}" +
		"pre{border:1px solid #ddd;border-radius:4px;overflow:auto}" +
		".ln{color:#999;user-select:none;display:inline-block;width:3.5em;text-align:right;margin-right:1em}" +
		".hit{background:#e6ffed}.miss{background:#ffdce0}</style>\n</head>\n<body>\n")
	fmt.Fprintf(&b, "<h1>Coverage</h1>\n<p>line %s · branch %s</p>\n", pct(doc.LineRate), pct(doc.BranchRate))

	// Summary table, each row anchoring to the source section below.
	b.WriteString("<table>\n<tr><th>Class</th><th>File</th><th>Line</th></tr>\n")
	for _, p := range doc.Packages {
		for _, c := range p.Classes {
			fmt.Fprintf(&b, "<tr><td>%s</td><td><a href=\"#%s\">%s</a></td><td>%s</td></tr>\n",
				html.EscapeString(c.Name), html.EscapeString(c.Filename),
				html.EscapeString(c.Filename), pct(c.LineRate))
		}
	}
	b.WriteString("</table>\n")

	// Per-file source listings with line highlighting where resolvable.
	for _, p := range doc.Packages {
		for _, c := range p.Classes {
			fmt.Fprintf(&b, "<h2 id=\"%s\">%s <small>(line %s)</small></h2>\n",
				html.EscapeString(c.Filename), html.EscapeString(c.Filename), pct(c.LineRate))
			src := resolveCoberturaSource(doc.Sources, c.Filename)
			body, rerr := os.ReadFile(src)
			if src == "" || rerr != nil {
				b.WriteString("<p><em>source not found — summary only</em></p>\n")
				continue
			}
			hits := map[int]int{}
			for _, l := range c.Lines {
				hits[l.Number] = l.Hits
			}
			b.WriteString("<pre>")
			for i, line := range strings.Split(string(body), "\n") {
				n := i + 1
				cls := ""
				if h, executable := hits[n]; executable {
					if h > 0 {
						cls = "hit"
					} else {
						cls = "miss"
					}
				}
				fmt.Fprintf(&b, "<span class=\"%s\"><span class=\"ln\">%d</span>%s</span>\n",
					cls, n, html.EscapeString(line))
			}
			b.WriteString("</pre>\n")
		}
	}
	b.WriteString("</body>\n</html>\n")

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", err
	}
	index := filepath.Join(targetDir, "index.html")
	if err := os.WriteFile(index, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return index, nil
}

// resolveCoberturaSource finds the on-disk source for a Cobertura class
// filename: the filename itself when absolute, else joined against each
// <source> root, returning the first that exists ("" when none resolve).
func resolveCoberturaSource(sources []string, filename string) string {
	if filename == "" {
		return ""
	}
	if filepath.IsAbs(filename) && fileExists(filename) {
		return filename
	}
	for _, s := range sources {
		if p := filepath.Join(s, filename); fileExists(p) {
			return p
		}
	}
	return ""
}

