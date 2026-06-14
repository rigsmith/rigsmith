package cli

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// The coverage summary table is the terminal-native counterpart to `--open`:
// after a coverage run, rig prints a per-file line-coverage table (worst-first,
// with a bar and a TOTAL row) so you see what needs attention without leaving
// the terminal. It's drawn from whatever the run already produced — Cobertura
// for .NET, the coverage profile for go, coverage-summary.json for node — so it
// never costs an extra command. It shows automatically on an interactive
// stdout; `rig coverage --no-summary` opts out.

// fileCov is one file's line coverage for the summary table.
type fileCov struct {
	name    string // display name (repo-relative where resolvable)
	covered int
	total   int
}

// pct is the file's line-coverage percentage; a file with no measurable lines
// counts as fully covered (so it never drags the worst-first ordering down).
func (f fileCov) pct() float64 {
	if f.total == 0 {
		return 100
	}
	return float64(f.covered) / float64(f.total) * 100
}

// coverageSummaryRows is the most files the table lists before collapsing the
// rest into a "… N more" line — enough to scan, not so many it scrolls away.
const coverageSummaryRows = 40

var coverageHeaderStyle = lipgloss.NewStyle().Bold(true)

// coverageTableEligible reports whether the summary table should be drawn: an
// interactive stdout, not silenced by --quiet, and not a dry run (where no
// coverage was actually produced).
func coverageTableEligible() bool {
	return !quiet && !dryRun && term.IsTerminal(os.Stdout.Fd())
}

// presentCoverage shows the post-run coverage view: the interactive browser
// when --browse is set (falling back to the static table if no per-line data
// can be assembled), otherwise the static summary table. goProfile is the go
// coverage profile path (go only; "" elsewhere).
func presentCoverage(cmd *cobra.Command, eco, root, goProfile string, summary, browse bool) {
	if browse {
		if runCoverageBrowser(cmd, eco, root, goProfile) {
			return
		}
		// No per-line data (e.g. node without lcov) — fall back to the table.
	}
	if summary || browse {
		showCoverageSummary(cmd, eco, root, goProfile)
	}
}

// showCoverageSummary renders the per-file coverage table for eco when one can
// be assembled from the run's artifacts. Best-effort and silent on miss — the
// table is a convenience, never a failure mode. goProfile is the go coverage
// profile path (only set for go runs); ignored for other ecosystems.
func showCoverageSummary(cmd *cobra.Command, eco, root, goProfile string) {
	if !coverageTableEligible() {
		return
	}
	files, ok := coverageFiles(eco, root, goProfile)
	if !ok || len(files) == 0 {
		return
	}
	fmt.Fprint(cmd.OutOrStdout(), renderCoverageTable(files))
}

// coverageFiles assembles per-file coverage for eco from the artifacts the run
// produced. The bool is false when the source artifact is missing/unreadable
// (distinct from "present but empty").
func coverageFiles(eco, root, goProfile string) ([]fileCov, bool) {
	switch eco {
	case detect.DotNet:
		c := findNewestCobertura(root)
		if c == "" {
			return nil, false
		}
		data, err := os.ReadFile(c)
		if err != nil {
			return nil, false
		}
		var doc coberturaDoc
		if xml.Unmarshal(data, &doc) != nil {
			return nil, false
		}
		return coberturaFileCov(doc), true
	case detect.Go:
		if goProfile == "" {
			return nil, false
		}
		data, err := os.ReadFile(goProfile)
		if err != nil {
			return nil, false
		}
		return goProfileFileCov(string(data), root), true
	case detect.Node:
		data, err := os.ReadFile(filepath.Join(root, "coverage", "coverage-summary.json"))
		if err != nil {
			return nil, false
		}
		return nodeSummaryFileCov(data, root), true
	}
	return nil, false
}

// coberturaFileCov aggregates a Cobertura document into per-file line coverage,
// merging the (possibly several) classes that share a filename. Pure.
func coberturaFileCov(doc coberturaDoc) []fileCov {
	// Dedupe by line number (max hits), exactly like coberturaDetail: Cobertura
	// reports (notably C#/ReportGenerator) emit one <class> per method, so a
	// source file's lines repeat across several class blocks. Counting per <line>
	// element would inflate the denominator and skew the percentage.
	byFile := map[string]map[int]int{} // file → line number → max hits
	var order []string
	for _, p := range doc.Packages {
		for _, c := range p.Classes {
			name := c.Filename
			if name == "" {
				name = c.Name
			}
			m, ok := byFile[name]
			if !ok {
				m = map[int]int{}
				byFile[name] = m
				order = append(order, name)
			}
			for _, l := range c.Lines {
				if cur, seen := m[l.Number]; !seen || l.Hits > cur {
					m[l.Number] = l.Hits
				}
			}
		}
	}
	out := make([]fileCov, 0, len(order))
	for _, name := range order {
		var covered, total int
		for _, hits := range byFile[name] {
			total++
			if hits > 0 {
				covered++
			}
		}
		out = append(out, fileCov{name: name, covered: covered, total: total})
	}
	return out
}

// parseGoProfile parses a `go test -coverprofile` profile into per-file line
// hits, flattening statement blocks to line granularity (a line takes the max
// hit count of any block touching it). Returns the file→line→hits map and the
// files in first-seen order. Pure.
func parseGoProfile(profile string) (map[string]map[int]int, []string) {
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
	return files, order
}

// goProfileFileCov parses a go coverage profile into per-file line coverage.
// Names are made repo-relative where the module path resolves under root. Pure.
func goProfileFileCov(profile, root string) []fileCov {
	files, order := parseGoProfile(profile)
	out := make([]fileCov, 0, len(order))
	for _, file := range order {
		covered := 0
		for _, hits := range files[file] {
			if hits > 0 {
				covered++
			}
		}
		out = append(out, fileCov{name: goDisplayName(file, root), covered: covered, total: len(files[file])})
	}
	return out
}

// goDisplayName turns a go profile's import-path-qualified file
// ("github.com/me/mod/pkg/f.go") into something short: the path tail after the
// repo's module path when it can be resolved, else the last two segments.
func goDisplayName(file, root string) string {
	if mod := goModulePath(root); mod != "" {
		if rel := strings.TrimPrefix(file, mod+"/"); rel != file {
			return rel
		}
	}
	parts := strings.Split(file, "/")
	if len(parts) > 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return file
}

// goModulePath reads the `module` path from root/go.mod (""=none/unreadable).
func goModulePath(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// nodeSummaryFileCov parses Istanbul's coverage-summary.json into per-file line
// coverage. Keys are absolute file paths plus a "total" rollup, which is
// skipped; paths are made repo-relative against root. Pure.
func nodeSummaryFileCov(data []byte, root string) []fileCov {
	var doc map[string]struct {
		Lines struct {
			Total   int `json:"total"`
			Covered int `json:"covered"`
		} `json:"lines"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	names := make([]string, 0, len(doc))
	for k := range doc {
		if k != "total" {
			names = append(names, k)
		}
	}
	sort.Strings(names) // map order is random; stabilize before the table sorts
	out := make([]fileCov, 0, len(names))
	for _, name := range names {
		l := doc[name].Lines
		display := name
		if rel, err := filepath.Rel(root, name); err == nil && !strings.HasPrefix(rel, "..") {
			display = filepath.ToSlash(rel)
		}
		out = append(out, fileCov{name: display, covered: l.Covered, total: l.Total})
	}
	return out
}

// renderCoverageTable formats the per-file coverage table: a header, files
// sorted worst-first (so the lowest coverage is at the top), and a TOTAL row.
// Long lists collapse to the worst coverageSummaryRows with a "… N more" note.
// Returns a trailing-newline-terminated block.
func renderCoverageTable(files []fileCov) string {
	sorted := append([]fileCov(nil), files...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if a, b := sorted[i].pct(), sorted[j].pct(); a != b {
			return a < b
		}
		return sorted[i].name < sorted[j].name
	})

	var totCovered, totLines int
	for _, f := range files {
		totCovered += f.covered
		totLines += f.total
	}
	totalLabel := fmt.Sprintf("TOTAL (%d files)", len(files))

	shown := sorted
	hidden := 0
	if len(shown) > coverageSummaryRows {
		hidden = len(shown) - coverageSummaryRows
		shown = shown[:coverageSummaryRows]
	}

	// Name column width: the widest shown name (incl. the TOTAL label), clamped.
	nameW := len(totalLabel)
	for _, f := range shown {
		if n := len(f.name); n > nameW {
			nameW = n
		}
	}
	if nameW > 50 {
		nameW = 50
	}

	var b strings.Builder
	b.WriteByte('\n')
	b.WriteString(coverageHeaderStyle.Render("Coverage summary") + "\n")
	for _, f := range shown {
		b.WriteString(coverageRow(f.name, f.pct(), nameW, false))
	}
	if hidden > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  … %d more file(s)", hidden)) + "\n")
	}
	overall := fileCov{covered: totCovered, total: totLines}
	b.WriteString(coverageRow(totalLabel, overall.pct(), nameW, true))
	return b.String()
}

// coverageRow renders one "name  bar  pct%" line, colored by coverage band.
// total rows are emphasized; the name is left-ellipsized (the tail of a path is
// the informative part) to fit nameW.
func coverageRow(name string, pct float64, nameW int, total bool) string {
	label := ellipsizeLeft(name, nameW)
	style := coverageBandStyle(pct)
	bar := style.Render(coverageBar(pct, 10))
	pctStr := style.Render(fmt.Sprintf("%5.1f%%", pct))
	nameCol := fmt.Sprintf("%-*s", nameW, label)
	if total {
		nameCol = coverageHeaderStyle.Render(nameCol)
	}
	return fmt.Sprintf("  %s  %s  %s\n", nameCol, bar, pctStr)
}

// coverageBar renders a width-cell block bar filled proportionally to pct.
func coverageBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct/100*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// coverageBandStyle colors a row by coverage band: green ≥80, yellow ≥50, red
// below. Mirrors the thresholds most coverage tools use for at-a-glance health.
func coverageBandStyle(pct float64) lipgloss.Style {
	switch {
	case pct >= 80:
		return okStyle
	case pct >= 50:
		return lipgloss.NewStyle().Foreground(brandYellow)
	default:
		return failStyle
	}
}

// ellipsizeLeft trims a string to max runes from the LEFT, prefixing "…", so
// the (more informative) tail of a path survives. Pure.
func ellipsizeLeft(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return "…" + string(r[len(r)-(max-1):])
}
