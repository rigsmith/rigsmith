package cli

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/internal/rig/detect"
	"github.com/spf13/cobra"
)

// `rig coverage --browse` is the in-terminal counterpart to --open: an
// interactive browser over the same coverage the run produced. The file list
// (worst-covered first) is selectable; pressing enter opens the file's source
// with each executable line marked covered/uncovered and its hit count, so you
// can see exactly which lines went untested without opening a browser. Per-line
// data comes from Cobertura (.NET), the coverage profile (go), or lcov.info
// (node); files whose source can't be located still show their line ledger.

// covFile is one file's per-line coverage for the browser detail view.
type covFile struct {
	name  string      // display name (repo-relative where resolvable)
	path  string      // on-disk source path, "" when it couldn't be located
	lines map[int]int // executable line number → hit count
}

func (f covFile) covered() int {
	n := 0
	for _, h := range f.lines {
		if h > 0 {
			n++
		}
	}
	return n
}

func (f covFile) total() int { return len(f.lines) }

func (f covFile) pct() float64 {
	if f.total() == 0 {
		return 100
	}
	return float64(f.covered()) / float64(f.total()) * 100
}

// coverageDetail assembles per-file, per-line coverage for eco from the run's
// artifacts, resolving each file's on-disk source where it can. The bool is
// false when the source artifact is missing/unreadable. goProfile is the go
// coverage profile path (go only).
func coverageDetail(eco, root, goProfile string) ([]covFile, bool) {
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
		return coberturaDetail(doc), true
	case detect.Go:
		if goProfile == "" {
			return nil, false
		}
		data, err := os.ReadFile(goProfile)
		if err != nil {
			return nil, false
		}
		return goProfileDetail(string(data), root), true
	case detect.Node:
		data, err := os.ReadFile(filepath.Join(root, "coverage", "lcov.info"))
		if err != nil {
			return nil, false
		}
		return parseLcov(data, root), true
	}
	return nil, false
}

// coberturaDetail turns a Cobertura document into per-file line ledgers, merging
// classes that share a filename (max hits per line) and resolving each source
// from the report's <sources> roots. Pure aside from the source-existence probe.
func coberturaDetail(doc coberturaDoc) []covFile {
	byFile := map[string]map[int]int{}
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
	out := make([]covFile, 0, len(order))
	for _, name := range order {
		out = append(out, covFile{
			name:  name,
			path:  resolveCoberturaSource(doc.Sources, name),
			lines: byFile[name],
		})
	}
	return out
}

// goProfileDetail turns a go coverage profile into per-file line ledgers and
// resolves each file's source on disk via the workspace's module map.
func goProfileDetail(profile, root string) []covFile {
	files, order := parseGoProfile(profile)
	mods := goModuleDirs(root)
	out := make([]covFile, 0, len(order))
	for _, file := range order {
		out = append(out, covFile{
			name:  goDisplayName(file, root),
			path:  goResolveSource(file, mods),
			lines: files[file],
		})
	}
	return out
}

// goModuleDirs maps each workspace module's import path to its on-disk dir,
// reading go.work's `use` entries (falling back to root as a lone module).
func goModuleDirs(root string) map[string]string {
	dirs := []string{root}
	if data, err := os.ReadFile(filepath.Join(root, "go.work")); err == nil {
		for _, m := range goWorkUse.FindAllStringSubmatch(string(data), -1) {
			dirs = append(dirs, filepath.Join(root, filepath.FromSlash(m[1])))
		}
	}
	out := map[string]string{}
	for _, dir := range dirs {
		if mod := goModulePath(dir); mod != "" {
			out[mod] = dir
		}
	}
	return out
}

// goWorkUse matches `./mod` paths in a go.work use block.
var goWorkUse = regexp.MustCompile(`(?m)(?:^|\s)(\./[^\s()]+)`)

// goResolveSource maps a profile's import-path-qualified file to an on-disk
// path using the module map (longest matching module path wins). "" when none
// match or the file isn't on disk.
func goResolveSource(file string, mods map[string]string) string {
	bestLen := -1
	best := ""
	for mod, dir := range mods {
		if file == mod || strings.HasPrefix(file, mod+"/") {
			if len(mod) > bestLen {
				bestLen, best = len(mod), filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(file, mod)))
			}
		}
	}
	if best != "" && fileExists(best) {
		return best
	}
	return ""
}

// parseLcov parses an lcov.info into per-file line ledgers: SF: starts a record
// (the source path), DA:<line>,<hits> records a line, end_of_record closes it.
// Paths are made repo-relative for display and resolved on disk. Pure aside
// from the source-existence probe.
func parseLcov(data []byte, root string) []covFile {
	var out []covFile
	var cur *covFile
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "SF:"):
			sf := strings.TrimSpace(line[3:])
			path := resolveNodeSource(sf, root)
			cur = &covFile{name: nodeDisplayPath(sf, root), path: path, lines: map[int]int{}}
		case strings.HasPrefix(line, "DA:") && cur != nil:
			parts := strings.SplitN(line[3:], ",", 3) // line,hits[,checksum]
			if len(parts) < 2 {
				continue
			}
			ln, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
			hits, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err1 == nil && err2 == nil {
				if h, seen := cur.lines[ln]; !seen || hits > h {
					cur.lines[ln] = hits // keep the higher count if a line repeats
				}
			}
		case line == "end_of_record":
			if cur != nil {
				out = append(out, *cur)
				cur = nil
			}
		}
	}
	if cur != nil { // tolerate a missing final end_of_record
		out = append(out, *cur)
	}
	return out
}

// resolveNodeSource finds an lcov SF: path on disk: absolute as-is, else joined
// against root. "" when it doesn't exist.
func resolveNodeSource(sf, root string) string {
	if sf == "" {
		return ""
	}
	if filepath.IsAbs(sf) {
		if fileExists(sf) {
			return sf
		}
		return ""
	}
	if p := filepath.Join(root, sf); fileExists(p) {
		return p
	}
	return ""
}

// nodeDisplayPath renders an lcov path repo-relative where it sits under root.
func nodeDisplayPath(sf, root string) string {
	if rel, err := filepath.Rel(root, sf); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return sf
}

// ---- the browser (bubbletea) -------------------------------------------

var (
	covCursorStyle = lipgloss.NewStyle().Foreground(brandCyan).Bold(true)
	covGutterStyle = lipgloss.NewStyle().Foreground(brandMuted)
	covMissBgStyle = lipgloss.NewStyle().Foreground(brandRed)
	covHitMark     = lipgloss.NewStyle().Foreground(brandGreen).Render("│")
	covMissMark    = lipgloss.NewStyle().Foreground(brandRed).Render("│")
)

// covBrowser is the bubbletea model: a selectable file list that drills into a
// scrollable, per-line-annotated source view.
type covBrowser struct {
	files  []covFile // sorted worst-first
	cursor int
	nameW  int
	detail bool // false = list, true = source view
	vp     viewport.Model
	ready  bool
	w, h   int
}

func newCovBrowser(files []covFile) covBrowser {
	sort.SliceStable(files, func(i, j int) bool {
		if a, b := files[i].pct(), files[j].pct(); a != b {
			return a < b
		}
		return files[i].name < files[j].name
	})
	nameW := len("TOTAL")
	for _, f := range files {
		if n := len(f.name); n > nameW {
			nameW = n
		}
	}
	if nameW > 50 {
		nameW = 50
	}
	return covBrowser{files: files, nameW: nameW}
}

func (m covBrowser) Init() tea.Cmd { return nil }

func (m covBrowser) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		vpH := msg.Height - 2 // header + footer
		if vpH < 3 {
			vpH = 3
		}
		if !m.ready {
			m.vp = viewport.New(msg.Width, vpH)
			m.ready = true
		} else {
			m.vp.Width, m.vp.Height = msg.Width, vpH
		}
		return m, nil
	case tea.KeyMsg:
		if m.detail {
			switch msg.String() {
			case "esc", "h", "left", "backspace":
				m.detail = false
				return m, nil
			case "q", "ctrl+c":
				return m, tea.Quit
			}
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.files)-1 {
				m.cursor++
			}
		case "home", "g":
			m.cursor = 0
		case "end", "G":
			m.cursor = len(m.files) - 1
		case "enter", "l", "right":
			if len(m.files) > 0 {
				m.openDetail()
			}
		}
	}
	return m, nil
}

// openDetail renders the selected file's annotated source into the viewport.
func (m *covBrowser) openDetail() {
	f := m.files[m.cursor]
	m.vp.SetContent(renderSourceDetail(f))
	m.vp.GotoTop()
	m.detail = true
}

func (m covBrowser) View() string {
	if !m.ready {
		return "loading…"
	}
	if m.detail {
		f := m.files[m.cursor]
		header := coverageHeaderStyle.Render(ellipsizeLeft(f.name, m.w-24)) +
			"  " + coverageBandStyle(f.pct()).Render(fmt.Sprintf("%.1f%%", f.pct()))
		footer := dimStyle.Render("↑/↓ scroll · esc back · q quit")
		return header + "\n" + m.vp.View() + "\n" + footer
	}

	var b strings.Builder
	b.WriteString(coverageHeaderStyle.Render("Coverage — select a file") + "\n")
	// Window the list to the visible height (header + footer = 2 lines).
	rows := m.h - 2
	if rows < 1 {
		rows = len(m.files)
	}
	start := 0
	if m.cursor >= rows {
		start = m.cursor - rows + 1
	}
	end := start + rows
	if end > len(m.files) {
		end = len(m.files)
	}
	for i := start; i < end; i++ {
		f := m.files[i]
		cursor := "  "
		if i == m.cursor {
			cursor = covCursorStyle.Render("▸ ")
		}
		style := coverageBandStyle(f.pct())
		name := fmt.Sprintf("%-*s", m.nameW, ellipsizeLeft(f.name, m.nameW))
		if i == m.cursor {
			name = covCursorStyle.Render(name)
		}
		fmt.Fprintf(&b, "%s%s  %s  %s\n", cursor, name,
			style.Render(coverageBar(f.pct(), 10)), style.Render(fmt.Sprintf("%5.1f%%", f.pct())))
	}
	b.WriteString(dimStyle.Render("↑/↓ move · enter open · q quit"))
	return b.String()
}

// renderSourceDetail builds the per-line annotated source for a file: each
// source line prefixed with its number, a covered/uncovered marker, and the hit
// count for executable lines; uncovered lines are tinted red. When the source
// can't be located it falls back to the bare line→hits ledger.
func renderSourceDetail(f covFile) string {
	if f.path == "" {
		return renderLedger(f)
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		return renderLedger(f)
	}
	var b strings.Builder
	for i, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		n := i + 1
		num := covGutterStyle.Render(fmt.Sprintf("%5d", n))
		hits, executable := f.lines[n]
		switch {
		case !executable:
			fmt.Fprintf(&b, "%s       %s\n", num, line)
		case hits > 0:
			fmt.Fprintf(&b, "%s %s %s %s\n", num, covHitMark, covGutterStyle.Render(fmt.Sprintf("%4d", hits)), line)
		default:
			fmt.Fprintf(&b, "%s %s %s %s\n", num, covMissMark, covGutterStyle.Render("   ✗"), covMissBgStyle.Render(line))
		}
	}
	return b.String()
}

// renderLedger is the fallback when source isn't on disk: the executable lines
// and their hit counts, sorted, with uncovered ones flagged.
func renderLedger(f covFile) string {
	nums := make([]int, 0, len(f.lines))
	for n := range f.lines {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	var b strings.Builder
	b.WriteString(dimStyle.Render("source not found — line ledger only") + "\n\n")
	for _, n := range nums {
		hits := f.lines[n]
		if hits > 0 {
			fmt.Fprintf(&b, "%s %s %s\n", covGutterStyle.Render(fmt.Sprintf("%5d", n)), covHitMark, covGutterStyle.Render(fmt.Sprintf("%d hit(s)", hits)))
		} else {
			fmt.Fprintf(&b, "%s %s %s\n", covGutterStyle.Render(fmt.Sprintf("%5d", n)), covMissMark, covMissBgStyle.Render("not covered"))
		}
	}
	return b.String()
}

// runCoverageBrowser launches the interactive browser for eco. It returns false
// (having drawn nothing) when no per-line data can be assembled, so the caller
// can fall back to the static summary table.
func runCoverageBrowser(cmd *cobra.Command, eco, root, goProfile string) bool {
	files, ok := coverageDetail(eco, root, goProfile)
	if !ok || len(files) == 0 {
		return false
	}
	prog := tea.NewProgram(newCovBrowser(files),
		tea.WithAltScreen(), tea.WithInput(cmd.InOrStdin()), tea.WithOutput(cmd.OutOrStdout()))
	if _, err := prog.Run(); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), dimStyle.Render("coverage browser: "+err.Error()))
		return false
	}
	return true
}
