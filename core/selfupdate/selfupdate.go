// Package selfupdate is the `self-update [--check]` verb every rigsmith tool
// carries. The tools ship together — one GoReleaser run, one tag, one
// scripts/install.sh — so they update together: run from any of them, it finds
// the latest release, reads the version of each tool installed beside the
// running binary, and re-runs the installer once for the whole family, pinned
// to that tag and pointed at that directory. A tool already at the tag is
// re-installed anyway, so the family always ends on one version.
//
// Originally rig's port of the .NET rig's UpdateVerb, which handed the install
// to the distribution's own updater (`dotnet tool update`); here that updater
// is the curl|sh installer.
package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/core/semver"
)

// Family is every binary scripts/install.sh installs, in its install order.
// Keep it in step with install.sh's `all`.
var Family = []string{"rig", "shiprig", "clauderig", "codexrig", "changerig", "brewrig"}

// DefaultRepo is the GitHub repo self-update checks and installs from,
// matching scripts/install.sh. $RIG_SELFUPDATE_REPO overrides it at run time.
const DefaultRepo = "rigsmith/rigsmith"

// RepoEnv names the run-time override of DefaultRepo.
const RepoEnv = "RIG_SELFUPDATE_REPO"

// Options configures the verb for one tool.
type Options struct {
	// Tool is the running binary's name ("rig", "shiprig", …).
	Tool string
	// Version is the running binary's stamped version; "dev" for a source build.
	Version string
	// Run executes the installer argv. Nil runs it directly, wired to the
	// command's output; rig passes its own runner so --dry-run and the command
	// echo apply.
	Run func(cmd *cobra.Command, argv []string) error
}

// NewCmd builds the `self-update` verb for one tool.
func NewCmd(o Options) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update every rigsmith tool to the latest release",
		Long: "Check GitHub Releases for the latest rigsmith release and install it for the\n" +
			"whole family — rig, shiprig, clauderig, codexrig, changerig and brewrig —\n" +
			"by re-running the rigsmith installer (scripts/install.sh) once, pinned to\n" +
			"that release and pointed at the directory this binary runs from. Every tool\n" +
			"ends on the same version, including one that was already current.\n\n" +
			"With --check it only reports each tool's installed version against the\n" +
			"latest release.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return Run(cmd.OutOrStdout(), check, liveEnv(cmd, o))
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report each tool's version against the latest release")
	return cmd
}

// Env is everything Run reads from the outside world, so the flow is testable
// without a network, a real install or a real bin directory.
type Env struct {
	Tool    string
	Version string
	GOOS    string
	Repo    string
	// Exe is the running binary's path as invoked; Resolved is it with
	// symlinks followed ("" when unknown).
	Exe, Resolved string
	// FetchLatestTag returns the latest release tag ("" when there is none).
	FetchLatestTag func() (string, error)
	// ProbeVersion reports the version printed by the binary at path, and
	// whether a binary is there at all.
	ProbeVersion func(path string) (version string, present bool)
	// Install runs the installer argv.
	Install func(argv []string) error
}

// goos is runtime.GOOS, a variable so it reads as one seam with the others.
var goos = runtime.GOOS

var dim = lipgloss.NewStyle().Foreground(brand.Muted)

// Run is the verb: check, report, and (unless check) install.
func Run(out io.Writer, check bool, e Env) error {
	current := trimV(e.Version)
	// A source ("dev") build isn't managed by the installer — nothing to update.
	if IsDevVersion(current) {
		fmt.Fprintf(out, "this %s is a dev build (not an installed release); nothing to self-update.\n", e.Tool)
		fmt.Fprintln(out, dim.Render("install a release with: curl -fsSL https://raw.githubusercontent.com/"+e.Repo+"/main/scripts/install.sh | sh"))
		return nil
	}

	// A package manager's install belongs to it: writing into its bin would
	// replace its links with copies it no longer tracks — and an npm install
	// keeps each tool in its own package, so one bin directory isn't the family.
	if upgrade, manager, note := ManagedUpgrade(e.Tool, e.Resolved); upgrade != "" {
		fmt.Fprintf(out, "this %s was installed by %s; update it with:\n", e.Tool, manager)
		fmt.Fprintln(out, "  "+upgrade)
		fmt.Fprintln(out, dim.Render(note))
		return nil
	}

	tag, err := e.FetchLatestTag()
	if err != nil {
		// Offline / rate-limited is a degraded check, not a failure of the tool.
		fmt.Fprintln(out, dim.Render("couldn't reach GitHub to check for updates: "+err.Error()))
		return nil
	}
	if tag == "" {
		fmt.Fprintln(out, dim.Render("no published releases found for "+e.Repo+"."))
		return nil
	}
	latest := trimV(tag)

	binDir := filepath.Dir(e.Exe)
	tools := FamilyFor(e.GOOS)
	before := Discover(binDir, tools, e.Tool, current, e.ProbeVersion)

	if check {
		fmt.Fprintf(out, "latest release: v%s\n", latest)
		for _, line := range CheckLines(before, latest) {
			fmt.Fprintln(out, "  "+line)
		}
	}

	// Never step the running tool back: a build newer than the latest release
	// (a prerelease, say) is left alone, and so is its family.
	if newerThan(current, latest) {
		fmt.Fprintf(out, "%s v%s is newer than the latest release (v%s); nothing to update.\n", e.Tool, current, latest)
		return nil
	}
	if !NeedsUpdate(before, latest) {
		fmt.Fprintf(out, "the rigsmith tools are up to date (v%s).\n", latest)
		return nil
	}

	if check {
		fmt.Fprintf(out, "run `%s self-update` to install v%s for every tool.\n", e.Tool, latest)
		return nil
	}

	if e.GOOS == "windows" {
		// The installer is POSIX sh; on Windows a running .exe is locked
		// anyway. Point at the release instead of half-failing.
		fmt.Fprintf(out, "self-update can't replace a running %s.exe on Windows.\n", e.Tool)
		fmt.Fprintln(out, dim.Render("download "+tag+" from https://github.com/"+e.Repo+"/releases/latest"))
		return nil
	}

	fmt.Fprintf(out, "updating the rigsmith tools in %s to v%s…\n", binDir, latest)
	if err := e.Install(InstallerArgs(e.Repo, tag, binDir)); err != nil {
		return err
	}

	after := Discover(binDir, tools, "", "", e.ProbeVersion)
	for _, line := range MoveLines(before, after) {
		fmt.Fprintln(out, "  "+line)
	}
	return nil
}

// Installed is one family member as found in the bin directory.
type Installed struct {
	Tool    string
	Present bool
	// Version is what the binary reported, without a leading v; "" when it
	// printed nothing recognisable as a version.
	Version string
}

// FamilyFor is Family on goos: brewrig only exists where Homebrew does, and
// install.sh skips it on Windows. Pure.
func FamilyFor(goos string) []string {
	out := make([]string, 0, len(Family))
	for _, t := range Family {
		if t == "brewrig" && goos == "windows" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Discover reads each tool's version from binDir — the directory the installer
// will write to, not whatever PATH happens to find first. The running tool is
// taken from its own stamp (self, selfVersion) rather than probed; pass self ""
// to probe every tool. Pure given probe.
func Discover(binDir string, tools []string, self, selfVersion string, probe func(path string) (string, bool)) []Installed {
	out := make([]Installed, 0, len(tools))
	for _, t := range tools {
		if t == self {
			out = append(out, Installed{Tool: t, Present: true, Version: trimV(selfVersion)})
			continue
		}
		v, present := probe(filepath.Join(binDir, t))
		out = append(out, Installed{Tool: t, Present: present, Version: ParseVersionOutput(v)})
	}
	return out
}

// ParseVersionOutput reads the version from a tool's `--version` output. A
// release prints its version first ("1.27.0", or "v1.27.0 (abc1234)"); a
// source build prints "source build · 2026-06-17 14:30 · …" and has none, so
// only a first word shaped major.minor.patch counts. Without a leading v; ""
// when there is no version. Pure.
func ParseVersionOutput(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	f := trimV(fields[0])
	if !releaseShaped.MatchString(f) {
		return ""
	}
	if _, ok := semver.Parse(f); !ok {
		return ""
	}
	return f
}

var releaseShaped = regexp.MustCompile(`^\d+\.\d+\.\d+([-+]|$)`)

// NeedsUpdate reports whether any installed tool is off latest — older, newer,
// or unreadable — so the family would not end on one version without a run.
// Tools that aren't installed don't count. Pure.
func NeedsUpdate(tools []Installed, latest string) bool {
	latest = trimV(latest)
	for _, t := range tools {
		if t.Present && t.Version != latest {
			return true
		}
	}
	return false
}

// CheckLines is the --check report: one line per tool, its installed version
// (or "not installed") against latest. Pure.
func CheckLines(tools []Installed, latest string) []string {
	latest = trimV(latest)
	width := nameWidth(tools)
	lines := make([]string, 0, len(tools))
	for _, t := range tools {
		var state string
		switch {
		case !t.Present:
			state = "not installed"
		case t.Version == "":
			state = "unknown version → v" + latest
		case t.Version == latest:
			state = "v" + t.Version + " (current)"
		default:
			state = "v" + t.Version + " → v" + latest
		}
		lines = append(lines, fmt.Sprintf("%-*s  %s", width, t.Tool, state))
	}
	return lines
}

// MoveLines reports what an install changed, tool by tool, from before to
// after: "v1.23.1 → v1.28.0", "installed v1.28.0", "v1.28.0 (unchanged)", or
// "not installed" for a tool the installer didn't leave behind. Pure.
func MoveLines(before, after []Installed) []string {
	prev := make(map[string]Installed, len(before))
	for _, b := range before {
		prev[b.Tool] = b
	}
	width := nameWidth(after)
	lines := make([]string, 0, len(after))
	for _, a := range after {
		b := prev[a.Tool]
		var state string
		switch {
		case !a.Present:
			state = "not installed"
		case !b.Present:
			state = "installed " + showVersion(a.Version)
		case b.Version == a.Version:
			state = showVersion(a.Version) + " (unchanged)"
		default:
			state = showVersion(b.Version) + " → " + showVersion(a.Version)
		}
		lines = append(lines, fmt.Sprintf("%-*s  %s", width, a.Tool, state))
	}
	return lines
}

// InstallerArgs is the argv that re-runs the rigsmith installer for the whole
// family, pinned to tag and installing into binDir. Pure.
//
// The script is fetched from the tag too, not from main: what runs is the
// installer that shipped with the release it installs, and a later change to
// main can't change what a self-update executes.
//
// RIGSMITH_BIN_DIR names the directory outright. RIGSMITH_INSTALL (its parent,
// when binDir is a …/bin) says the same thing to an installer that predates
// RIGSMITH_BIN_DIR, which only knows the prefix. RIGSMITH_REPO keeps a
// $RIG_SELFUPDATE_REPO fork's installer downloading that fork's release.
func InstallerArgs(repo, tag, binDir string) []string {
	vars := []string{
		"RIGSMITH_VERSION=" + shellQuote(tag),
		"RIGSMITH_REPO=" + shellQuote(repo),
		"RIGSMITH_BIN_DIR=" + shellQuote(binDir),
	}
	if filepath.Base(binDir) == "bin" {
		vars = append(vars, "RIGSMITH_INSTALL="+shellQuote(filepath.Dir(binDir)))
	}
	script := "curl -fsSL https://raw.githubusercontent.com/" + repo + "/" + tag + "/scripts/install.sh" +
		" | " + strings.Join(vars, " ") + " sh -s all"
	return []string{"sh", "-c", script}
}

// ManagedUpgrade reports how to update tool when its resolved binary belongs
// to a package manager: the command, the manager's name, and a note about the
// rest of the family. All "" when no manager owns it. Pure.
//
// Homebrew is named by the cask or formula the path is in — the `rigsmith`
// bundle cask, or a per-tool one like `rig` — since brew upgrades only the
// token it is given. npm keeps each tool in its own package, so it is named
// per tool.
func ManagedUpgrade(tool, resolved string) (upgrade, manager, note string) {
	p := filepath.ToSlash(resolved)
	if token := pathSegmentAfter(p, "/Caskroom/"); token != "" {
		return "brew upgrade --cask " + token, "Homebrew", brewNote(token)
	}
	if formula := pathSegmentAfter(p, "/Cellar/"); formula != "" {
		return "brew upgrade " + formula, "Homebrew", brewNote(formula)
	}
	if strings.Contains(p, "/node_modules/") {
		return "npm install -g @rigsmith/" + tool + "@latest", "npm",
			"each rigsmith tool is its own npm package; update the others you installed the same way."
	}
	return "", "", ""
}

func brewNote(token string) string {
	if token == "rigsmith" {
		return "the rigsmith cask carries every tool, so this updates the whole family."
	}
	return "tools installed as their own casks update by name: brew upgrade --cask <tool>."
}

// pathSegmentAfter is the path segment following marker in p, or "". Pure.
func pathSegmentAfter(p, marker string) string {
	i := strings.Index(p, marker)
	if i < 0 {
		return ""
	}
	seg, _, _ := strings.Cut(p[i+len(marker):], "/")
	return seg
}

// IsDevVersion reports whether v is a source build's version rather than a
// release's. Pure.
func IsDevVersion(v string) bool {
	v = trimV(v)
	return v == "" || v == "dev"
}

// ResolveRepo picks the GitHub repo slug: the env override when set, else
// DefaultRepo. Pure.
func ResolveRepo(env string) string {
	if s := strings.TrimSpace(env); s != "" {
		return s
	}
	return DefaultRepo
}

// LatestReleaseURL is the GitHub API endpoint for a repo's latest release. Pure.
func LatestReleaseURL(repo string) string {
	return "https://api.github.com/repos/" + repo + "/releases/latest"
}

// ParseReleaseTag extracts tag_name from a GitHub release JSON payload.
// "" when the payload is malformed or carries no tag. Pure.
func ParseReleaseTag(data []byte) string {
	var doc struct {
		TagName string `json:"tag_name"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	return doc.TagName
}

// FetchLatestReleaseTag GETs the release endpoint and returns its tag_name.
func FetchLatestReleaseTag(url string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub responded %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return ParseReleaseTag(body), nil
}

// liveEnv wires Env to the real world for one invocation.
func liveEnv(cmd *cobra.Command, o Options) Env {
	repo := ResolveRepo(os.Getenv(RepoEnv))
	exe, _ := os.Executable()
	resolved := ""
	if exe != "" {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			resolved = r
		}
	}
	run := o.Run
	if run == nil {
		run = execInstaller
	}
	return Env{
		Tool:           o.Tool,
		Version:        o.Version,
		GOOS:           goos,
		Repo:           repo,
		Exe:            exe,
		Resolved:       resolved,
		FetchLatestTag: func() (string, error) { return FetchLatestReleaseTag(LatestReleaseURL(repo)) },
		ProbeVersion: func(path string) (string, bool) {
			// os.Stat adds no extension, so a Windows sibling is found by its .exe.
			if goos == "windows" && filepath.Ext(path) == "" {
				path += ".exe"
			}
			return probeVersion(cmd.Context(), path)
		},
		Install: func(argv []string) error { return run(cmd, argv) },
	}
}

// probeVersion runs `<path> --version`. present is false when no file is there.
func probeVersion(ctx context.Context, path string) (string, bool) {
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return "", true
	}
	return string(out), true
}

// execInstaller runs argv with the command's output and the terminal's input.
func execInstaller(cmd *cobra.Command, argv []string) error {
	c := exec.CommandContext(cmd.Context(), argv[0], argv[1:]...)
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	c.Stdin = os.Stdin
	return c.Run()
}

// newerThan reports whether a is a strictly newer release than b. Unparseable
// versions never count as newer.
func newerThan(a, b string) bool {
	av, ok := semver.Parse(trimV(a))
	if !ok {
		return false
	}
	bv, ok := semver.Parse(trimV(b))
	if !ok {
		return false
	}
	return semver.Compare(av, bv) > 0
}

func showVersion(v string) string {
	if v == "" {
		return "unknown version"
	}
	return "v" + v
}

func nameWidth(tools []Installed) int {
	w := 0
	for _, t := range tools {
		w = max(w, len(t.Tool))
	}
	return w
}

func trimV(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

// shellQuote single-quotes s for sh. Pure.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
