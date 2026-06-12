package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/clauderig/internal/config"
	"github.com/rigsmith/clauderig/internal/redact"
	"github.com/rigsmith/core/pathmap"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// cliOnlyConfig points the cli root at a synthetic dir and disables desktop.
func cliOnlyConfig(cliDir string) *config.Config {
	c := config.Default()
	c.Roots = []config.Root{
		{ID: "cli", Enabled: true, Location: pathmap.Cascade{Portable: cliDir}},
	}
	return c
}

func TestSync_RedactsCopiesAndBuildsManifest(t *testing.T) {
	live := t.TempDir()
	write(t, live, "settings.json", `{"effortLevel":"high","apiKey":"sk-ant-abcdefgh12345678"}`)
	write(t, live, "skills/x/SKILL.md", "skill body")
	write(t, live, "statsig/junk", "should not sync")
	write(t, live, "projects/-Users-john-Git-rigsmith/s.jsonl",
		`{"type":"user","cwd":"/Users/john/Git/rigsmith","isSidechain":false}`+"\n")

	staging := t.TempDir()
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: staging, Config: cliOnlyConfig(live), Machine: m, ClaudeVersion: "2.1.175"})
	if err != nil {
		t.Fatal(err)
	}

	// settings.json copied + redacted
	got := read(t, filepath.Join(staging, "cli", "settings.json"))
	if !contains(got, redact.Placeholder) || contains(got, "sk-ant-") {
		t.Errorf("settings not redacted: %s", got)
	}
	// skill copied verbatim
	if read(t, filepath.Join(staging, "cli", "skills", "x", "SKILL.md")) != "skill body" {
		t.Error("skill not copied")
	}
	// junk excluded
	if _, err := os.Stat(filepath.Join(staging, "cli", "statsig", "junk")); err == nil {
		t.Error("statsig junk should not have synced")
	}
	// transcript copied
	if _, err := os.Stat(filepath.Join(staging, "cli", "projects", "-Users-john-Git-rigsmith", "s.jsonl")); err != nil {
		t.Error("transcript not copied")
	}
	// manifest built with the portable template
	if rep.ManifestProjects != 1 {
		t.Fatalf("manifest projects = %d", rep.ManifestProjects)
	}
	man := read(t, filepath.Join(staging, "clauderig-manifest.json"))
	if !contains(man, "$HOME/Git/rigsmith") {
		t.Errorf("manifest missing template: %s", man)
	}
	if len(rep.Findings) != 0 {
		t.Errorf("unexpected tripwire findings: %v", rep.Findings)
	}
}

func TestSync_TripwireBlocksLeak(t *testing.T) {
	live := t.TempDir()
	// A secret in a DEEPER json (not field-redacted) must trip the wire.
	write(t, live, "plugins/data/leak.json", `{"saved":"ghp_aaaaaaaaaaaaaaaaaaaaa"}`)

	staging := t.TempDir()
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: staging, Config: cliOnlyConfig(live), Machine: m})
	if err == nil {
		t.Fatal("expected tripwire error")
	}
	if len(rep.Findings) == 0 || !contains(rep.Findings[0].Path, "plugins/data/leak.json") {
		t.Fatalf("findings = %v", rep.Findings)
	}
}

func TestSync_SkipsAbsentRoot(t *testing.T) {
	c := config.Default()
	c.Roots = []config.Root{
		{ID: "cli", Enabled: true, Location: pathmap.Cascade{Portable: "/no/such/dir/here"}},
	}
	m := config.Machine{Name: "mbp", OS: pathmap.OSMacOS, Home: "/Users/john"}
	rep, err := Sync(Options{StagingDir: t.TempDir(), Config: c, Machine: m})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Roots) != 1 || !rep.Roots[0].Skipped {
		t.Fatalf("expected skipped root, got %+v", rep.Roots)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
