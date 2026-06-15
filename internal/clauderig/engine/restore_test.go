package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
)

// targetRootConfig points the cli root straight at targetDir (decoupling the
// write location from the machine's home, so slug math uses a realistic home).
func targetRootConfig(targetDir string) *config.Config {
	c := config.Default()
	c.Roots = []config.Root{{ID: "cli", Enabled: true, Location: pathmap.Cascade{Portable: targetDir}}}
	return c
}

func TestRestore_RewritesSlugAndMergesSecret(t *testing.T) {
	staging := t.TempDir()
	// staged set as Sync would produce it: redacted settings + a transcript under
	// the SOURCE slug, plus a manifest with the portable template.
	write(t, staging, "cli/settings.json", `{"apiKey":"__CLAUDERIG_REDACTED__","effortLevel":"high"}`)
	write(t, staging, "cli/projects/-Users-john-Git-rigsmith/s.jsonl", "transcript\n")
	m := &manifest.Manifest{
		Schema: 1, SourceOS: pathmap.OSMacOS,
		Projects: map[string]manifest.Project{
			"-Users-john-Git-rigsmith": {Template: "$HOME/Git/rigsmith", Cwd: "/Users/john/Git/rigsmith"},
		},
	}

	target := t.TempDir()
	// a pre-existing local settings.json with a REAL secret that must survive
	write(t, target, "settings.json", `{"apiKey":"sk-ant-LOCAL-real","effortLevel":"low"}`)

	// restoring onto "jane" on macOS → home /Users/jane
	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	rep, err := Restore(RestoreOptions{StagingDir: staging, Config: targetRootConfig(target), Machine: jane, Manifest: m, TargetOverride: override("cli", target)})
	if err != nil {
		t.Fatal(err)
	}

	// transcript landed under jane's slug, not john's
	janeSlug := filepath.Join(target, "projects", "-Users-jane-Git-rigsmith", "s.jsonl")
	if _, err := os.Stat(janeSlug); err != nil {
		t.Errorf("transcript not under target slug: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "projects", "-Users-john-Git-rigsmith")); err == nil {
		t.Error("source slug should not exist on target")
	}

	// local secret preserved; synced non-secret applied
	got := read(t, filepath.Join(target, "settings.json"))
	if !contains(got, "sk-ant-LOCAL-real") {
		t.Errorf("local secret clobbered: %s", got)
	}
	if !contains(got, `"effortLevel": "high"`) {
		t.Errorf("synced value not applied: %s", got)
	}

	if rep.Roots[0].SlugsRewritten != 1 {
		t.Errorf("slugs rewritten = %d, want 1", rep.Roots[0].SlugsRewritten)
	}
}

func TestRestore_FreshMachineDropsPlaceholder(t *testing.T) {
	staging := t.TempDir()
	write(t, staging, "cli/settings.json", `{"apiKey":"__CLAUDERIG_REDACTED__","effortLevel":"high"}`)
	target := t.TempDir() // no local settings.json

	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	if _, err := Restore(RestoreOptions{StagingDir: staging, Config: targetRootConfig(target), Machine: jane, TargetOverride: override("cli", target)}); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(target, "settings.json"))
	if contains(got, "REDACTED") {
		t.Errorf("placeholder leaked onto fresh machine: %s", got)
	}
	if !contains(got, `"effortLevel": "high"`) {
		t.Errorf("non-secret not restored: %s", got)
	}
}

func TestRestore_UnmappedSlugKept(t *testing.T) {
	staging := t.TempDir()
	write(t, staging, "cli/projects/-opt-shared/s.jsonl", "x\n")
	m := &manifest.Manifest{
		Schema: 1, SourceOS: pathmap.OSMacOS,
		Projects: map[string]manifest.Project{"-opt-shared": {Cwd: "/opt/shared"}}, // no template
	}
	target := t.TempDir()
	jane := config.Machine{Name: "jane", OS: pathmap.OSMacOS, Home: "/Users/jane"}
	rep, _ := Restore(RestoreOptions{StagingDir: staging, Config: targetRootConfig(target), Machine: jane, Manifest: m, TargetOverride: override("cli", target)})
	if _, err := os.Stat(filepath.Join(target, "projects", "-opt-shared", "s.jsonl")); err != nil {
		t.Errorf("unmapped slug should be kept as-is: %v", err)
	}
	if rep.Roots[0].SlugsRewritten != 0 {
		t.Errorf("nothing should be rewritten, got %d", rep.Roots[0].SlugsRewritten)
	}
}
