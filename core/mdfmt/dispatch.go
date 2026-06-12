package mdfmt

import (
	"os"
	"path/filepath"
	"strings"
)

// Formatter dispatch, ported from net-changesets' ChangelogFormatter: routes
// the `format` config value to the built-in native formatter, an auto-detected
// tool, or an explicit external formatter run through the repo's package
// manager. Every failure degrades to a warning — formatting never fails the
// version command.

// Runner executes a command in dir and returns its combined output. Injectable
// so dispatch is unit-testable without real toolchains.
type Runner func(dir, name string, args ...string) (string, error)

// WarnFunc receives human-readable degradation warnings.
type WarnFunc func(format string, a ...any)

// The formatters @changesets supports and the config files that auto-detect
// them, in detection order (mirrors the C# s_formatters table).
var formatters = []struct {
	name        string
	configFiles []string
}{
	{"dprint", []string{"dprint.json", "dprint.jsonc", ".dprint.json", ".dprint.jsonc"}},
	{"deno", []string{"deno.json", "deno.jsonc"}},
	{"oxfmt", []string{".oxfmtrc.json", ".oxfmtrc.jsonc", "oxfmt.config.ts"}},
	{"biome", []string{"biome.json", "biome.jsonc", ".biome.json", ".biome.jsonc"}},
	{"prettier", []string{
		".prettierrc", ".prettierrc.json", ".prettierrc.yml", ".prettierrc.yaml", ".prettierrc.json5",
		".prettierrc.js", "prettier.config.js", ".prettierrc.ts", "prettier.config.ts", ".prettierrc.mjs",
		"prettier.config.mjs", ".prettierrc.mts", "prettier.config.mts", ".prettierrc.cjs",
		"prettier.config.cjs", ".prettierrc.cts", "prettier.config.cts", ".prettierrc.toml",
	}},
}

const nativeName = "native"

// FormatFiles formats the given changelog files according to the resolved
// `format` setting: "" (config false/absent) and an empty file list are
// no-ops; "native" rewrites in process; "auto" detects a tool from config
// files in workingDir; a known tool name runs through the package manager
// resolved from lockfiles ("deno fmt" runs direct); an unknown name warns and
// does nothing.
func FormatFiles(files []string, format, workingDir string, run Runner, warnf WarnFunc) {
	if warnf == nil {
		warnf = func(string, ...any) {}
	}
	if len(files) == 0 {
		return
	}
	formatter := resolveFormatter(format, workingDir)
	if formatter == "" {
		return
	}

	// The built-in native formatter needs no Node, no package manager, and no
	// subprocess — the path that lets a repo with no JS toolchain still get a
	// formatted changelog.
	if strings.EqualFold(formatter, nativeName) {
		formatNatively(files, warnf)
		return
	}

	canonical := ""
	for _, f := range formatters {
		if strings.EqualFold(f.name, formatter) {
			canonical = f.name
			break
		}
	}
	if canonical == "" {
		warnf("Unknown formatter %q; leaving changelogs unformatted.", formatter)
		return
	}

	name, args := buildCommand(canonical, files, workingDir)
	if _, err := run(workingDir, name, args...); err != nil {
		warnf("The %q formatter failed (%v); changelogs may be unformatted.", formatter, err)
	}
}

// FormatFilesCustom runs a user-supplied formatter command (the config's
// `format: ["mytool", "--write"]` array form) with the changelog paths
// appended. The argv runs as written in workingDir — no package-manager
// wrapping, no name table. Same degradation contract as FormatFiles: empty
// inputs are no-ops and failures only warn.
func FormatFilesCustom(files, argv []string, workingDir string, run Runner, warnf WarnFunc) {
	if warnf == nil {
		warnf = func(string, ...any) {}
	}
	if len(files) == 0 || len(argv) == 0 {
		return
	}
	args := append(append([]string{}, argv[1:]...), files...)
	if _, err := run(workingDir, argv[0], args...); err != nil {
		warnf("The custom formatter %q failed (%v); changelogs may be unformatted.", argv[0], err)
	}
}

func formatNatively(files []string, warnf WarnFunc) {
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			warnf("Could not format %q; left as written.", file)
			continue
		}
		formatted := Format(string(content))
		if formatted == string(content) {
			continue // do not touch already-formatted files
		}
		if err := os.WriteFile(file, []byte(formatted), 0o644); err != nil {
			warnf("Could not format %q; left as written.", file)
		}
	}
}

func resolveFormatter(format, workingDir string) string {
	if format == "" {
		return ""
	}
	if strings.EqualFold(format, "auto") {
		return detect(workingDir)
	}
	return format
}

func detect(workingDir string) string {
	for _, f := range formatters {
		for _, cf := range f.configFiles {
			if _, err := os.Stat(filepath.Join(workingDir, cf)); err == nil {
				return f.name
			}
		}
	}
	return ""
}

func buildCommand(formatter string, files []string, workingDir string) (string, []string) {
	// deno is its own runtime, not a package-manager binary.
	if formatter == "deno" {
		return "deno", append([]string{"fmt"}, files...)
	}

	var tool []string
	switch formatter {
	case "prettier":
		tool = []string{"prettier", "--write"}
	case "biome":
		tool = []string{"@biomejs/biome", "format", "--write"}
	case "oxfmt":
		tool = []string{"oxfmt", "--write"}
	case "dprint":
		tool = []string{"dprint", "fmt"}
	default:
		tool = []string{formatter}
	}
	tool = append(tool, files...)

	name, prefix := resolvePackageManagerExec(workingDir)
	if prefix == "" {
		return name, tool
	}
	return name, append([]string{prefix}, tool...)
}

func resolvePackageManagerExec(workingDir string) (name, prefix string) {
	exists := func(f string) bool {
		_, err := os.Stat(filepath.Join(workingDir, f))
		return err == nil
	}
	switch {
	case exists("pnpm-lock.yaml"):
		return "pnpm", "exec"
	case exists("yarn.lock"):
		return "yarn", ""
	case exists("bun.lockb"), exists("bun.lock"):
		return "bun", "x"
	default:
		return "npx", ""
	}
}
