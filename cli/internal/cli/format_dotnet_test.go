package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDotnetFormatterIsCsharpier_Config(t *testing.T) {
	// Explicit config wins over convention.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".csharpierrc"), "{}") // convention says csharpier…
	writeFile(t, filepath.Join(root, ".rig.json"), `{ "dotnet": { "formatter": "dotnet" } }`)
	if dotnetFormatterIsCsharpier(root) {
		t.Error("explicit dotnet.formatter=dotnet should override a .csharpierrc")
	}

	root = t.TempDir()
	writeFile(t, filepath.Join(root, ".rig.json"), `{ "dotnet": { "formatter": "csharpier" } }`)
	if !dotnetFormatterIsCsharpier(root) {
		t.Error("explicit dotnet.formatter=csharpier should select csharpier")
	}
}

func TestDotnetFormatterIsCsharpier_Convention(t *testing.T) {
	// No config, no opt-in → dotnet format.
	if dotnetFormatterIsCsharpier(t.TempDir()) {
		t.Error("a plain repo should default to dotnet format")
	}

	// A .csharpierrc opts in.
	rc := t.TempDir()
	writeFile(t, filepath.Join(rc, ".csharpierrc.json"), "{}")
	if !dotnetFormatterIsCsharpier(rc) {
		t.Error(".csharpierrc.json should opt into csharpier")
	}

	// A tool-manifest entry opts in.
	mani := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mani, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(mani, ".config", "dotnet-tools.json"),
		`{"version":1,"isRoot":true,"tools":{"csharpier":{"version":"1.0.0","commands":["csharpier"]}}}`)
	if !manifestHasCsharpier(mani) {
		t.Error("tool-manifest with csharpier should be detected")
	}
	if !dotnetFormatterIsCsharpier(mani) {
		t.Error("a csharpier tool-manifest should opt into csharpier")
	}
}

func TestDotnetFormatArgv(t *testing.T) {
	// Not selected → no override (caller uses dotnet format).
	if _, ok := dotnetFormatArgv(t.TempDir()); ok {
		t.Error("a plain repo should not override the formatter")
	}

	// Selected but not installed → canonical `csharpier format .`.
	rc := t.TempDir()
	writeFile(t, filepath.Join(rc, ".csharpierrc"), "{}")
	argv, ok := dotnetFormatArgv(rc)
	if !ok {
		t.Fatal("csharpier should be selected")
	}
	// Either the canonical `csharpier …` (not installed / on PATH) or the
	// manifest form — but always the format subcommand on the current dir.
	if len(argv) < 2 || argv[len(argv)-2] != "format" || argv[len(argv)-1] != "." {
		t.Fatalf("argv should end with `format .`, got %v", argv)
	}
}
