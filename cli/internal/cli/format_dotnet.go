package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rigsmith/cli/internal/config"
	"github.com/rigsmith/cli/internal/detect"
	"github.com/spf13/cobra"
)

// CSharpier (https://csharpier.com) is an opinionated C# formatter — the
// Prettier-for-C# alternative to `dotnet format`. It's an external dotnet tool,
// so it slots into the extTool pattern (offer-to-install, doctor Tools group).
var toolCsharpier = extTool{
	name:    "csharpier",
	why:     "formats C# for `rig format`",
	install: []string{"dotnet", "tool", "install", "-g", "csharpier"},
	// Resolved like ReportGenerator: a `csharpier` on PATH (global tool) or a
	// local tool-manifest (`dotnet csharpier`). The install command handles the
	// absent case, so no fetch-on-use mode is needed.
	resolve: func(root, _ string) ([]string, bool) { return resolveCsharpier(root) },
}

// dotnetFormatterIsCsharpier reports whether `rig format` should use CSharpier
// for the .NET repo at root. Explicit `dotnet.formatter` wins; "auto" (default)
// uses CSharpier when the repo opts in — a `.csharpierrc*` config file or a
// dotnet tool-manifest that declares it — else `dotnet format`.
func dotnetFormatterIsCsharpier(root string) bool {
	cfg, _ := config.LoadMerged(root)
	switch strings.ToLower(strings.TrimSpace(cfg.DotnetFormatter)) {
	case "csharpier":
		return true
	case "dotnet", "dotnet-format":
		return false
	default: // "auto" / unset → convention
		return csharpierConfigPresent(root) || manifestHasCsharpier(root)
	}
}

// dotnetFormatArgv returns the CSharpier format command when CSharpier is the
// selected .NET formatter (ok=false → the caller uses `dotnet format`). When
// CSharpier is selected but not yet installed it returns the canonical
// `csharpier format .` so the command is well-formed; the run path's
// requireDotnetFormatter handles install/guidance.
func dotnetFormatArgv(root string) ([]string, bool) {
	if !dotnetFormatterIsCsharpier(root) {
		return nil, false
	}
	inv, ok := resolveCsharpier(root)
	if !ok {
		inv = []string{"csharpier"}
	}
	return append(inv, "format", "."), true
}

// requireDotnetFormatter ensures the selected .NET formatter is available before
// `rig format` runs it: CSharpier (an external tool) is offered for install on a
// TTY, else a guidance error; `dotnet format` is in-box, so a no-op. Non-.NET
// ecosystems and the dotnet-format choice are no-ops.
func requireDotnetFormatter(cmd *cobra.Command, eco, root string) error {
	if eco != detect.DotNet || !dotnetFormatterIsCsharpier(root) {
		return nil
	}
	_, err := toolCsharpier.require(cmd, root)
	return err
}

// resolveCsharpier reports how to invoke CSharpier: a `csharpier` on PATH (a
// global tool) or, failing that, a local tool-manifest entry (`dotnet
// csharpier`). The format-subcommand/args are appended by the caller.
func resolveCsharpier(root string) ([]string, bool) {
	if p, err := exec.LookPath("csharpier"); err == nil {
		return []string{p}, true
	}
	if manifestHasCsharpier(root) {
		return []string{"dotnet", "csharpier"}, true
	}
	return nil, false
}

// csharpierConfigNames are the files whose presence signals a repo uses
// CSharpier (its rc config, or the ignore file).
var csharpierConfigNames = []string{
	".csharpierrc", ".csharpierrc.json", ".csharpierrc.yaml", ".csharpierrc.yml", ".csharpierignore",
}

// csharpierConfigPresent reports whether a CSharpier config/ignore file exists
// at or above root.
func csharpierConfigPresent(root string) bool {
	dir := root
	for {
		for _, name := range csharpierConfigNames {
			if exists(filepath.Join(dir, name)) {
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

// manifestHasCsharpier reports whether a dotnet tool manifest at or above root
// declares CSharpier.
func manifestHasCsharpier(root string) bool {
	dir := root
	for {
		for _, rel := range []string{filepath.Join(".config", "dotnet-tools.json"), "dotnet-tools.json"} {
			if toolManifestDeclaresCsharpier(filepath.Join(dir, rel)) {
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

// toolManifestDeclaresCsharpier parses a dotnet-tools.json and reports whether
// any tool is CSharpier (package id contains "csharpier" or a command is
// "csharpier"). Best-effort: unreadable/garbled → false.
func toolManifestDeclaresCsharpier(path string) bool {
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
		if strings.Contains(strings.ToLower(id), "csharpier") {
			return true
		}
		for _, c := range tool.Commands {
			if strings.EqualFold(c, "csharpier") {
				return true
			}
		}
	}
	return false
}
