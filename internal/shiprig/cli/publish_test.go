package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
)

// publishEnvRepo lays out a one-package .NET workspace whose NUGET_API_KEY lives
// only in .env, with a fake `dotnet` first on PATH that logs every invocation.
// It returns the log path. OIDC is turned off in config so the run takes the
// API-key path deterministically (a CI runner with an OIDC context would
// otherwise try to mint a token instead).
func publishEnvRepo(t *testing.T) (logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake `dotnet` on PATH is a shell script")
	}
	root := t.TempDir()
	logPath = filepath.Join(root, "dotnet.log")

	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".changeset/config.json", `{ "dotnet": { "oidc": "off" } }`)
	write("src/Acme.Widgets/Acme.Widgets.csproj",
		"<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <PackageId>Acme.Widgets</PackageId>\n    <Version>1.2.3</Version>\n  </PropertyGroup>\n</Project>\n")
	write(".env", "NUGET_API_KEY=from-dotenv\n")

	bin := filepath.Join(root, "fakebin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + logPath + "\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "dotnet"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// The key must not be in the ambient environment: that is the whole point of
	// the test, and applyReleaseEnv mutates this process's env. t.Setenv records
	// the pre-test state, so the unset below is restored when the test ends.
	t.Setenv("NUGET_API_KEY", "")
	if err := os.Unsetenv("NUGET_API_KEY"); err != nil {
		t.Fatal(err)
	}

	t.Chdir(root)
	return logPath
}

// TestPublishReadsKeyFromDotenv pins the fix: a NUGET_API_KEY that exists only
// in .env reaches the registry push. It used to be invisible to `publish` (only
// `release` and `init` loaded .env), so the push went out with no --api-key.
func TestPublishReadsKeyFromDotenv(t *testing.T) {
	logPath := publishEnvRepo(t)

	cmd := newPublishCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--yes", "--no-git-tag"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("publish: %v", err)
	}

	push := dotnetPush(t, logPath)
	if !strings.Contains(push, "--api-key from-dotenv") {
		t.Errorf("push ran without the .env key: %q", push)
	}
}

// TestPublishNoEnvSuppressesDotenvKey pins the other half: --no-env drops the
// file layer, so the same key is not picked up and the push carries no key.
func TestPublishNoEnvSuppressesDotenvKey(t *testing.T) {
	logPath := publishEnvRepo(t)
	noEnv = true
	t.Cleanup(func() { noEnv = false }) // package-level flag var, shared across tests

	cmd := newPublishCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--yes", "--no-git-tag"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("publish: %v", err)
	}

	push := dotnetPush(t, logPath)
	if strings.Contains(push, "--api-key") {
		t.Errorf("--no-env should not surface the .env key: %q", push)
	}
	if v, ok := os.LookupEnv("NUGET_API_KEY"); ok {
		t.Errorf("--no-env should not export NUGET_API_KEY, got %q", v)
	}
}

// dotnetPush returns the `nuget push` line the fake dotnet recorded.
func dotnetPush(t *testing.T, logPath string) string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("fake dotnet was never invoked: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "nuget push ") {
			return line
		}
	}
	t.Fatalf("no `nuget push` in the dotnet log:\n%s", data)
	return ""
}

func TestPackageSourceFor(t *testing.T) {
	// A per-ecosystem packageSource block overrides the built-in default.
	cfg, err := config.Parse([]byte(`{ "dotnet": { "packageSource": "github" }, "node": { "packageSource": "https://npm.acme.dev" } }`))
	if err != nil {
		t.Fatal(err)
	}
	if got := packageSourceFor(cfg, "dotnet"); got != "github" {
		t.Errorf("dotnet packageSource = %q, want github", got)
	}
	if got := packageSourceFor(cfg, "node"); got != "https://npm.acme.dev" {
		t.Errorf("node packageSource = %q, want the configured URL", got)
	}
	// No block for go → built-in default ("" for go).
	if got := packageSourceFor(cfg, "go"); got != "" {
		t.Errorf("go packageSource = %q, want empty", got)
	}

	// With no config blocks, dotnet falls back to its hardcoded "nuget" default.
	def := config.Default()
	if got := packageSourceFor(def, "dotnet"); got != "nuget" {
		t.Errorf("default dotnet packageSource = %q, want nuget", got)
	}
}
