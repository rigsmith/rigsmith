package engine

import (
	"testing"

	"github.com/rigsmith/rigsmith/core/pathmap"
	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
	"github.com/rigsmith/rigsmith/internal/clauderig/project"
)

// Two machines syncing into the same staging repo must both appear in the
// manifest — the union, not last-writer-wins.
func TestSync_MultiMachineManifestUnion(t *testing.T) {
	staging := t.TempDir()

	// machine A (home /Users/alice)
	liveA := t.TempDir()
	aCwd := "/Users/alice/Git/x"
	aSlug := project.Flatten(aCwd)
	write(t, liveA, "projects/"+aSlug+"/s.jsonl", `{"type":"user","cwd":"`+aCwd+`","isSidechain":false}`+"\n")
	A := config.Machine{Name: "alice", OS: pathmap.OSMacOS, Home: "/Users/alice"}
	if _, err := Sync(Options{StagingDir: staging, Config: cliOnlyConfig(liveA), Machine: A,
		SourceOverride: map[string]string{"cli": liveA}}); err != nil {
		t.Fatal(err)
	}

	// machine B (home /Users/bob) into the SAME staging
	liveB := t.TempDir()
	bCwd := "/Users/bob/Git/y"
	bSlug := project.Flatten(bCwd)
	write(t, liveB, "projects/"+bSlug+"/s.jsonl", `{"type":"user","cwd":"`+bCwd+`","isSidechain":false}`+"\n")
	B := config.Machine{Name: "bob", OS: pathmap.OSMacOS, Home: "/Users/bob"}
	rep, err := Sync(Options{StagingDir: staging, Config: cliOnlyConfig(liveB), Machine: B,
		SourceOverride: map[string]string{"cli": liveB}})
	if err != nil {
		t.Fatal(err)
	}

	man, err := manifest.Load(staging)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := man.Projects[aSlug]; !ok {
		t.Errorf("machine A's project %q lost from manifest after B synced", aSlug)
	}
	if _, ok := man.Projects[bSlug]; !ok {
		t.Errorf("machine B's project %q missing", bSlug)
	}
	if rep.ManifestProjects != 2 {
		t.Errorf("ManifestProjects = %d, want 2 (union)", rep.ManifestProjects)
	}
}
