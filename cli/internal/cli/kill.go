package cli

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/rigsmith/core/ecosystem"
	"github.com/rigsmith/core/plugin"
	"github.com/spf13/cobra"
)

// killWarnStyle highlights the "would kill" header in dry-run output.
var killWarnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))

// newKillCmd builds `rig kill [name] [--port N]` — terminate running dev/app
// processes. It mirrors the .NET/Node `rig kill` verbs:
//
//   - `rig kill`              — sweep: kill processes whose command line matches
//     the repo's project names (or `.rig.json` kill.match, if set).
//   - `rig kill <name>`       — narrow to a project/pattern; a bare numeric arg
//     (`rig kill 3000`) is treated as a --port instead.
//   - `rig kill --port N`     — free whatever is LISTENING on those TCP ports
//     (repeatable). Port mode takes precedence over pattern mode.
//   - `--dry-run`/`-n`        — print what WOULD be killed without killing. The
//     root command already owns the persistent --dry-run flag; we read it.
//
// On darwin/linux it uses `lsof` (by port) and `pgrep`/`pkill -f` (by command
// line). On Windows it parses `netstat -ano` for ports and matches patterns
// against full command lines via CIM (Get-CimInstance Win32_Process through
// PowerShell), falling back to image-name `taskkill /IM` when PowerShell is
// unreachable.
func newKillCmd() *cobra.Command {
	var ports []int

	cmd := &cobra.Command{
		Use:   "kill [name]",
		Short: "Terminate running dev/app processes",
		Long: "Kill running dev/app processes by command-line pattern or TCP port.\n\n" +
			"  rig kill            sweep the repo's projects (or .rig.json kill.match)\n" +
			"  rig kill <name>     narrow to a project/pattern\n" +
			"  rig kill 3000       a bare number is treated as --port\n" +
			"  rig kill --port N   free whatever is listening on port N (repeatable)",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: workspaceNameCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			cwd, _ := os.Getwd()
			root := resolveRoot(cwd)
			// Read the root's persistent --dry-run rather than redefining it.
			dry, _ := cmd.Flags().GetBool("dry-run")

			var name string
			if len(args) == 1 {
				name = strings.TrimSpace(args[0])
			}

			// A bare numeric positional (`rig kill 3000`) is a port, matching the
			// Node/.NET tools.
			allPorts := append([]int(nil), ports...)
			if name != "" {
				if p, err := strconv.Atoi(name); err == nil && p > 0 {
					allPorts = append(allPorts, p)
					name = ""
				}
			}

			if len(allPorts) > 0 {
				return killByPorts(cmd, out, root, allPorts, dry)
			}

			cfg, _ := config.LoadMerged(root)
			patterns := resolveKillPatterns(cfg, root, name)
			if len(patterns) == 0 {
				fmt.Fprintln(out, dimStyle.Render(
					"nothing to kill: no kill.match patterns and no projects to infer from"))
				return nil
			}
			return killByPatterns(cmd, out, root, patterns, dry)
		},
	}

	cmd.Flags().IntSliceVar(&ports, "port", nil, "kill the process(es) listening on this TCP port (repeatable)")
	return cmd
}

// resolveKillPatterns builds the command-line substrings to match. Precedence
// (matching the .NET rig's KillVerb.ResolvePatterns):
//
//  1. `.rig.json` kill.match — wins outright when set, even over a name arg
//     (it may pin non-project processes like a hung test host).
//  2. an explicit `name` arg — resolve it like `rig run` would (exact project
//     name, else substring), falling back to the raw string so you can still
//     `rig kill SomeExternalProc`.
//  3. default sweep — every discovered package's Name (narrow, present in the
//     dev driver's command line); in a .NET repo only the RUNNABLE projects'
//     names (libs/tests never own a process). Falls back to the repo directory
//     name when no packages are discovered, so the sweep still does something.
func resolveKillPatterns(cfg config.Config, root, name string) []string {
	if len(cfg.Kill.Match) > 0 {
		return cfg.Kill.Match
	}
	// A .NET repo follows the .NET rig's resolution exactly: the project Name —
	// present in the `dotnet run --project …/Foo.csproj` driver's cmdline and in
	// the apphost path — is the target, never the AssemblyName.
	var dotnetProjects []detect.ProjectInfo
	if hasDotNet(root) {
		dotnetProjects = detect.DiscoverDotNet(root, cfg.Solution, cfg.Exclude)
	}
	if name != "" {
		if matched := dotnetKillPatterns(dotnetProjects, name); len(matched) > 0 {
			return matched
		}
		names := discoveredPackageNames(root, cfg.Exclude)
		if matched := matchProjectNames(names, name); len(matched) > 0 {
			return matched
		}
		return []string{name}
	}
	// Default sweep patterns are auto-derived, so guard against dangerously broad
	// matches: a 1–2 char name (e.g. a module called "ex") would `pgrep -f` half
	// the system. Drop anything shorter than 3 chars; if nothing survives, sweep
	// nothing (the user can pass an explicit name or kill.match).
	if len(dotnetProjects) > 0 {
		return safePatterns(dotnetKillPatterns(dotnetProjects, ""))
	}
	if names := safePatterns(discoveredPackageNames(root, cfg.Exclude)); len(names) > 0 {
		return names
	}
	if base := dirBase(root); len(base) >= 3 {
		return []string{base}
	}
	return nil
}

// dotnetKillPatterns mirrors the .NET rig's project-based pattern resolution
// over the discovered .NET projects: a named query resolves exact
// Name/ShortName first, then substring (nil when nothing matches, so the
// caller can fall back to the raw string); a bare kill sweeps every RUNNABLE
// project's Name — the "stop everything I started" sweep, libs/tests excluded
// and never the AssemblyName. Pure.
func dotnetKillPatterns(projects []detect.ProjectInfo, query string) []string {
	if query != "" {
		var exact, sub []string
		q := strings.ToLower(strings.TrimSpace(query))
		for _, p := range projects {
			switch {
			case strings.ToLower(p.Name) == q || strings.ToLower(p.ShortName()) == q:
				exact = append(exact, p.Name)
			case strings.Contains(strings.ToLower(p.Name), q):
				sub = append(sub, p.Name)
			}
		}
		if len(exact) > 0 {
			return exact
		}
		return sub
	}
	var names []string
	for _, p := range projects {
		if p.IsRunnable() {
			names = append(names, p.Name)
		}
	}
	return names
}

// safePatterns drops auto-derived patterns too short to match safely.
func safePatterns(patterns []string) []string {
	out := patterns[:0]
	for _, p := range patterns {
		if len(strings.TrimSpace(p)) >= 3 {
			out = append(out, p)
		}
	}
	return out
}

// matchProjectNames mirrors `rig run`'s resolution: an exact (case-insensitive)
// name match wins; otherwise every name containing the query (substring). Pure.
func matchProjectNames(names []string, query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var exact, sub []string
	for _, n := range names {
		ln := strings.ToLower(n)
		switch {
		case ln == q:
			exact = append(exact, n)
		case strings.Contains(ln, q):
			sub = append(sub, n)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return sub
}

// discoveredPackageNames asks the ecosystem registry which packages live at
// root and returns their Names (sorted, deduped), dropping any matching the
// `exclude` globs. Best-effort: discovery errors yield no names, and the caller
// falls back to the repo directory name.
func discoveredPackageNames(root string, exclude []string) []string {
	ctx := context.Background()
	seen := map[string]bool{}
	for _, eco := range ecosystem.Default().All() {
		if ok, _ := eco.Detect(ctx, root); !ok {
			continue
		}
		resp, err := eco.Discover(ctx, plugin.DiscoverRequest{RepoRoot: root, SourcePath: "."})
		if err != nil {
			continue
		}
		for _, p := range resp.Packages {
			if p.Name == "" || excluded(p.Name, exclude) || excluded(shortName(p.Name), exclude) {
				continue
			}
			seen[p.Name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// dirBase returns the final path element of root (the repo directory name).
func dirBase(root string) string {
	root = strings.TrimRight(root, "/\\")
	if i := strings.LastIndexAny(root, "/\\"); i >= 0 {
		return root[i+1:]
	}
	return root
}

// ---- Port mode: free whatever is LISTENING on the given TCP ports. ----

func killByPorts(cmd *cobra.Command, out io.Writer, root string, ports []int, dry bool) error {
	self := os.Getpid()
	seen := map[int]bool{}
	var pids []int
	for _, p := range ports {
		for _, pid := range listeningPids(cmd, root, p, self) {
			if !seen[pid] {
				seen[pid] = true
				pids = append(pids, pid)
			}
		}
	}
	sort.Ints(pids)
	portList := joinInts(ports, ", ")

	if len(pids) == 0 {
		fmt.Fprintln(out, dimStyle.Render("nothing listening on "+portList))
		return nil
	}
	if dry {
		fmt.Fprintln(out, killWarnStyle.Render(
			fmt.Sprintf("would kill %d process(es) on %s: %s", len(pids), portList, joinInts(pids, ", "))))
		return nil
	}

	killed := killPids(cmd, root, pids)
	if killed > 0 {
		fmt.Fprintln(out, fmt.Sprintf("killed %d process(es) on %s", killed, portList))
	}
	return nil
}

// listeningPids returns the PIDs with a LISTEN socket on port, excluding self.
// darwin/linux: `lsof -ti tcp:<port> -sTCP:LISTEN`. Windows: `netstat -ano`.
func listeningPids(cmd *cobra.Command, root string, port, self int) []int {
	if runtime.GOOS == "windows" {
		out, _ := capture(cmd, root, "netstat", "-ano", "-p", "tcp")
		return parseNetstatPids(out, port, self)
	}
	// lsof exits 1 with no output when nothing is listening — that's just "none".
	out, _ := capture(cmd, root, "lsof", "-ti", "tcp:"+strconv.Itoa(port), "-sTCP:LISTEN")
	return parsePids(out, self)
}

// parsePids turns whitespace-separated PID tokens (lsof -ti output) into a
// unique, ascending PID list, dropping self and non-positive values. Pure.
func parsePids(output string, self int) []int {
	seen := map[int]bool{}
	var pids []int
	for _, tok := range strings.Fields(output) {
		n, err := strconv.Atoi(tok)
		if err != nil || n <= 0 || n == self || seen[n] {
			continue
		}
		seen[n] = true
		pids = append(pids, n)
	}
	sort.Ints(pids)
	return pids
}

// parseNetstatPids extracts the trailing-token PID of each LISTENING row in
// `netstat -ano` output whose local address ends in :port, minus self. Pure.
func parseNetstatPids(output string, port, self int) []int {
	suffix := ":" + strconv.Itoa(port)
	seen := map[int]bool{}
	var pids []int
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || !strings.Contains(strings.ToUpper(line), "LISTENING") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Local address is the 2nd column in `netstat -ano` (Proto Local Foreign State PID).
		if !strings.HasSuffix(fields[1], suffix) {
			continue
		}
		n, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil || n <= 0 || n == self || seen[n] {
			continue
		}
		seen[n] = true
		pids = append(pids, n)
	}
	sort.Ints(pids)
	return pids
}

// killPids sends each PID a terminate signal and returns how many calls
// succeeded. darwin/linux: `kill -TERM`. Windows: `taskkill /PID <pid> /F`.
func killPids(cmd *cobra.Command, root string, pids []int) int {
	killed := 0
	for _, pid := range pids {
		var c *exec.Cmd
		if runtime.GOOS == "windows" {
			c = exec.CommandContext(cmd.Context(), "taskkill", "/PID", strconv.Itoa(pid), "/F", "/T")
		} else {
			c = exec.CommandContext(cmd.Context(), "kill", "-TERM", strconv.Itoa(pid))
		}
		c.Dir = root
		if err := c.Run(); err == nil {
			killed++
		}
	}
	return killed
}

// ---- Pattern mode: match the full command line. ----

func killByPatterns(cmd *cobra.Command, out io.Writer, root string, patterns []string, dry bool) error {
	if runtime.GOOS == "windows" {
		return killByPatternsWindows(cmd, out, root, patterns, dry)
	}

	if dry {
		any := false
		for _, pattern := range patterns {
			// pgrep -fl prints "<pid> <command line>" per match.
			lines := nonEmptyLines(captureOut(cmd, root, "pgrep", "-fl", pattern))
			if len(lines) == 0 {
				continue
			}
			any = true
			fmt.Fprintln(out, killWarnStyle.Render(
				fmt.Sprintf("would kill %d process(es) matching '%s':", len(lines), pattern)))
			for _, l := range lines {
				fmt.Fprintln(out, dimStyle.Render("  "+l))
			}
		}
		if !any {
			fmt.Fprintln(out, dimStyle.Render("no matching processes"))
		}
		return nil
	}

	killed := 0
	for _, pattern := range patterns {
		c := exec.CommandContext(cmd.Context(), "pkill", "-f", pattern)
		c.Dir = root
		// pkill exit 0 = killed something, 1 = no match (fine), >1 = error.
		if err := c.Run(); err == nil {
			killed++
			fmt.Fprintln(out, "killed process(es) matching '"+pattern+"'")
		}
	}
	if killed == 0 {
		fmt.Fprintln(out, dimStyle.Render("no matching processes"))
	}
	return nil
}

// killByPatternsWindows matches patterns against the FULL process command line
// (CIM Win32_Process via PowerShell — the .NET rig's KillVerb.ExecuteWindows),
// so the `dotnet run`/`dotnet watch` driver (image dotnet.exe) is caught
// alongside the apphost. When PowerShell/CIM can't be reached it falls back to
// the image-name taskkill path.
func killByPatternsWindows(cmd *cobra.Command, out io.Writer, root string, patterns []string, dry bool) error {
	procs, ok := windowsProcessList(cmd, root)
	if !ok {
		fmt.Fprintln(out, dimStyle.Render(
			"pattern-based kill is best-effort on Windows; prefer `rig kill --port <n>`"))
		return killByImageWindows(cmd, out, root, patterns, dry)
	}

	self := os.Getpid()
	alreadyKilled := map[int]bool{}
	for _, pattern := range patterns {
		matches := matchProcesses(procs, pattern, self)

		if dry {
			if len(matches) == 0 {
				fmt.Fprintln(out, dimStyle.Render(fmt.Sprintf("no process matches '%s'", pattern)))
				continue
			}
			fmt.Fprintln(out, killWarnStyle.Render(
				fmt.Sprintf("would kill %d process(es) matching '%s':", len(matches), pattern)))
			for _, p := range matches {
				fmt.Fprintln(out, dimStyle.Render(fmt.Sprintf("  %d  %s", p.Pid, p.CommandLine)))
			}
			continue
		}

		any := false
		for _, p := range matches {
			// A prior pattern's /T may already have taken this PID down in a tree.
			if alreadyKilled[p.Pid] {
				any = true
				continue
			}
			alreadyKilled[p.Pid] = true
			c := exec.CommandContext(cmd.Context(), "taskkill", "/F", "/T", "/PID", strconv.Itoa(p.Pid))
			c.Dir = root
			if err := c.Run(); err == nil {
				any = true
			}
		}
		if any {
			fmt.Fprintln(out, "killed process(es) matching '"+pattern+"'")
		} else {
			fmt.Fprintln(out, dimStyle.Render("no process matched '"+pattern+"'"))
		}
	}
	return nil
}

// processEntry is one running process: its PID and full command line.
type processEntry struct {
	Pid         int
	CommandLine string
}

// cimProcessListScript prints "PID<tab>CommandLine" per process. Silencing the
// progress stream keeps stdout clean of the CLIXML noise CIM writes.
const cimProcessListScript = "$ProgressPreference='SilentlyContinue'; " +
	"Get-CimInstance Win32_Process | ForEach-Object { \"$($_.ProcessId)`t$($_.CommandLine)\" }"

// windowsProcessList returns (PID, command line) for every process via the CIM
// query. ok=false when PowerShell/CIM can't be reached, so the caller can fall
// back to image-name matching.
func windowsProcessList(cmd *cobra.Command, root string) ([]processEntry, bool) {
	// -EncodedCommand sidesteps every nested-quoting pitfall; tab-delimited
	// output parses cleanly even when command lines contain spaces or quotes.
	output, err := capture(cmd, root, "powershell",
		"-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(cimProcessListScript))
	if err != nil && output == "" {
		return nil, false // couldn't start / produced nothing usable
	}
	return parseProcessList(output), true
}

// encodePowerShell encodes a script for powershell -EncodedCommand
// (base64 over UTF-16LE). Pure.
func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	buf := make([]byte, 0, len(units)*2)
	for _, u := range units {
		buf = append(buf, byte(u), byte(u>>8))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// parseProcessList parses the tab-delimited `PID<tab>CommandLine` lines the
// CIM query emits. Lines without a parseable PID are skipped; a missing
// command line (system processes) yields an empty string. Pure — the .NET
// rig's KillVerb.ParseProcessList.
func parseProcessList(output string) []processEntry {
	var result []processEntry
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimRight(raw, "\r")
		if line == "" {
			continue
		}
		pidText, cmdline := line, ""
		if tab := strings.IndexByte(line, '\t'); tab >= 0 {
			pidText, cmdline = line[:tab], line[tab+1:]
		}
		pid, err := strconv.Atoi(strings.TrimSpace(pidText))
		if err != nil {
			continue
		}
		result = append(result, processEntry{Pid: pid, CommandLine: cmdline})
	}
	return result
}

// matchProcesses returns the processes whose command line contains pattern
// (case-insensitive), excluding our own process and command-line-less system
// processes. Mirrors pkill -f's substring match. Pure — the .NET rig's
// KillVerb.MatchProcesses.
func matchProcesses(procs []processEntry, pattern string, self int) []processEntry {
	p := strings.ToLower(pattern)
	var out []processEntry
	for _, proc := range procs {
		if proc.Pid == self || proc.CommandLine == "" {
			continue
		}
		if strings.Contains(strings.ToLower(proc.CommandLine), p) {
			out = append(out, proc)
		}
	}
	return out
}

// killByImageWindows is the pre-CIM safety net: a best-effort image-name kill
// (taskkill /IM). It can't match the full command line the way pkill -f does.
func killByImageWindows(cmd *cobra.Command, out io.Writer, root string, patterns []string, dry bool) error {
	for _, pattern := range patterns {
		image := pattern
		if !strings.HasSuffix(strings.ToLower(image), ".exe") {
			image += ".exe"
		}
		if dry {
			fmt.Fprintln(out, killWarnStyle.Render("would taskkill /IM "+image))
			continue
		}
		c := exec.CommandContext(cmd.Context(), "taskkill", "/F", "/IM", image)
		c.Dir = root
		if err := c.Run(); err == nil {
			fmt.Fprintln(out, "killed process(es) matching '"+pattern+"'")
		}
	}
	return nil
}

// ---- small helpers ----

// capture runs argv in dir and returns its combined-ish stdout, ignoring the
// exit status (lsof/pgrep exit non-zero on "no match", which we treat as empty).
func capture(cmd *cobra.Command, dir, name string, args ...string) (string, error) {
	c := exec.CommandContext(cmd.Context(), name, args...)
	c.Dir = dir
	b, err := c.Output()
	return string(b), err
}

// captureOut is capture with the error dropped, for the dry-run reporters.
func captureOut(cmd *cobra.Command, dir, name string, args ...string) string {
	s, _ := capture(cmd, dir, name, args...)
	return s
}

// nonEmptyLines splits output into trimmed, non-empty lines.
func nonEmptyLines(output string) []string {
	var lines []string
	for _, raw := range strings.Split(output, "\n") {
		if l := strings.TrimSpace(raw); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// joinInts renders ints joined by sep.
func joinInts(xs []int, sep string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, sep)
}
