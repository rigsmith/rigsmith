package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// warnStyle marks warning-level doctor checks (yellow).
var warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))

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
// gate. It mirrors the .NET/Node `rig doctor` verbs.
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the environment for the detected ecosystem(s)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			cwd, _ := os.Getwd()
			root := detect.Root(cwd)

			ecos := detectEcosystems(root)
			fmt.Fprintln(out, headerStyle.Render("rig doctor")+"  "+dimStyle.Render(root))
			fmt.Fprintln(out)

			if len(ecos) == 0 {
				fmt.Fprintln(out, dimStyle.Render(
					"no recognized ecosystem (.NET/Node/Go/Cargo) found here — nothing to check"))
				return nil
			}

			severity := docOK
			emit := func(c check) {
				if c.level > severity {
					severity = c.level
				}
				fmt.Fprintln(out, "  "+renderMark(c.level)+" "+pad(c.label, 10)+" "+dimStyle.Render(c.detail))
			}

			for _, eco := range ecos {
				for _, c := range checksFor(cmd, eco, root) {
					emit(c)
				}
			}

			fmt.Fprintln(out)
			switch severity {
			case docOK:
				fmt.Fprintln(out, okStyle.Render("all good"))
			case docWarn:
				fmt.Fprintln(out, warnStyle.Render("some warnings"))
			default:
				fmt.Fprintln(out, failStyle.Render("problems found"))
			}
			if severity == docError {
				return fmt.Errorf("doctor: problems found")
			}
			return nil
		},
	}
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

// detectEcosystems lists the ecosystem ids that apply at root by checking
// manifests directly (dependency-free, no registry round-trip). A polyglot repo
// legitimately matches several.
func detectEcosystems(root string) []string {
	var ecos []string
	if exists(filepath.Join(root, "go.mod")) || exists(filepath.Join(root, "go.work")) {
		ecos = append(ecos, detect.Go)
	}
	if exists(filepath.Join(root, "package.json")) {
		ecos = append(ecos, detect.Node)
	}
	if exists(filepath.Join(root, "Cargo.toml")) {
		ecos = append(ecos, detect.Cargo)
	}
	if hasDotNet(root) {
		ecos = append(ecos, detect.DotNet)
	}
	return ecos
}

// checksFor runs the per-ecosystem environment checks.
func checksFor(cmd *cobra.Command, eco, root string) []check {
	switch eco {
	case detect.Go:
		return goChecks(cmd, root)
	case detect.Node:
		return nodeChecks(cmd, root)
	case detect.DotNet:
		return dotnetChecks(cmd, root)
	case detect.Cargo:
		return cargoChecks(cmd, root)
	}
	return nil
}

func goChecks(cmd *cobra.Command, root string) []check {
	var out []check
	if v, present := probeVersion(cmd, root, "go", "version"); present {
		out = append(out, ok2("go", strings.TrimPrefix(v, "go version ")))
	} else {
		out = append(out, bad("go", "go not found on PATH"))
	}
	switch {
	case exists(filepath.Join(root, "go.work")):
		out = append(out, ok("module", "go.work present"))
	case exists(filepath.Join(root, "go.mod")):
		out = append(out, ok("module", "go.mod present"))
	default:
		out = append(out, warn("module", "no go.mod/go.work found"))
	}
	return out
}

func nodeChecks(cmd *cobra.Command, root string) []check {
	var out []check
	if v, present := probeVersion(cmd, root, "node", "--version"); present {
		out = append(out, ok2("node", v))
	} else {
		out = append(out, bad("node", "node not found on PATH"))
	}

	pm := string(detect.DetectNodePM(root))
	if v, present := probeVersion(cmd, root, pm, "--version"); present {
		out = append(out, ok2("pm", pm+" "+v))
	} else {
		out = append(out, bad("pm", pm+" not found on PATH"))
	}

	if exists(filepath.Join(root, "package.json")) {
		out = append(out, ok("package", "package.json present"))
	} else {
		out = append(out, warn("package", "no package.json"))
	}

	if exists(filepath.Join(root, "node_modules")) {
		out = append(out, ok("install", "node_modules present"))
	} else {
		out = append(out, warn("install", "not installed — run `rig install`"))
	}
	return out
}

func dotnetChecks(cmd *cobra.Command, root string) []check {
	var out []check
	sdk, present := probeVersion(cmd, root, "dotnet", "--version")
	if !present {
		out = append(out, bad("sdk", "dotnet not found on PATH"))
	} else {
		pin := readSdkPin(root)
		switch {
		case pin == "":
			out = append(out, ok2("sdk", sdk))
		case sdkSatisfies(sdk, pin):
			out = append(out, ok2("sdk", fmt.Sprintf("%s (global.json pins %s)", sdk, pin)))
		default:
			out = append(out, bad("sdk", fmt.Sprintf("%s — global.json pins %s", sdk, pin)))
		}
	}

	if hasSolution(root) {
		out = append(out, ok("layout", "solution present"))
	} else if hasCsproj(root) {
		out = append(out, ok("layout", "project(s) present"))
	} else {
		out = append(out, warn("layout", "no solution or project found"))
	}
	return out
}

func cargoChecks(cmd *cobra.Command, root string) []check {
	var out []check
	if v, present := probeVersion(cmd, root, "cargo", "--version"); present {
		out = append(out, ok2("cargo", strings.TrimPrefix(v, "cargo ")))
	} else {
		out = append(out, bad("cargo", "cargo not found on PATH"))
	}
	if exists(filepath.Join(root, "Cargo.toml")) {
		out = append(out, ok("manifest", "Cargo.toml present"))
	} else {
		out = append(out, warn("manifest", "no Cargo.toml"))
	}
	return out
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
