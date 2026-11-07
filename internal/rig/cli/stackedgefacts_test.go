package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

func edgeSetupStackspace(t *testing.T) (*stackEdgeSetup, *edgeSetupAPI, *bytes.Buffer, string) {
	t.Helper()
	s, f, out := edgeSetupFixture(t)
	ws, fork, _ := trackStackspace(t, "stack/integration")
	m, _, err := loadStackManifest(ws)
	if err != nil {
		t.Fatal(err)
	}
	m.Repos["lib"].PublishPrefix = "You."
	m.Edge = s.m.Edge
	m.Repos["app"] = &stackRepo{Upstream: "github.com/you/App", Fork: "github.com/you/App", Owned: true}
	m.Proposals = map[string]map[string]stackProposal{"lib": {"stack-pr-fix-b": {Branch: "stack/fix-b"}}}
	data, _ := json.Marshal(m)
	writeStackManifest(t, ws, string(data))
	csproj(t, ws, "lib/Lib", "Lib")
	csproj(t, ws, "app/App", "App", "You.Lib")
	mustGitStack(t, ws, "add", ".")
	mustGitStack(t, ws, "commit", "-qm", "edge setup input")
	s.root = ws
	s.m = m
	home := filepath.Join(t.TempDir(), "home.git")
	mustGitStack(t, ws, "init", "--bare", "-b", "main", home)
	if err := stackConfigureHome(s.ctx, ws, home, "main", ""); err != nil {
		t.Fatal(err)
	}
	return s, f, out, fork
}

func TestStackEdgeFactsTrackBareRepos(t *testing.T) {
	s, _, _, fork := edgeSetupStackspace(t)
	if err := s.checkTrack("lib"); err == nil {
		t.Fatal("missing track branch passed")
	}
	if err := s.repairTrack("lib"); err != nil {
		t.Fatal(err)
	}
	if err := s.checkTrack("lib"); err != nil {
		t.Fatal(err)
	}
	receipt := mustGitStack(t, s.root, "rev-parse", "refs/rigsmith/integration/lib")
	mustGitStack(t, fork, "update-ref", "-d", "refs/heads/stack/integration")
	if err := s.checkTrack("lib"); err == nil {
		t.Fatal("missing remote branch passed with local receipt")
	}
	mustGitStack(t, fork, "update-ref", "refs/heads/stack/integration", strings.TrimSpace(receipt))
	mustGitStack(t, s.root, "switch", "-q", "-c", "isolated-topic", "HEAD~1")
	s.m.Proposals["lib"]["absent-topic"] = stackProposal{Branch: "stack/absent"}
	if err := s.checkTrack("lib"); err == nil {
		t.Fatal("missing topic passed")
	}
	delete(s.m.Proposals["lib"], "absent-topic")
	mustGitStack(t, s.root, "switch", "-q", "main")
	// A real topic not contained in HEAD must fail even when receipt and remote match.
	mustGitStack(t, s.root, "switch", "-q", "isolated-topic")
	mustGitStack(t, s.root, "commit", "--allow-empty", "-qm", "unmerged topic")
	mustGitStack(t, s.root, "switch", "-q", "main")
	s.m.Proposals["lib"]["isolated-topic"] = stackProposal{Branch: "stack/isolated"}
	if err := s.checkTrack("lib"); err == nil || !strings.Contains(err.Error(), "not an ancestor") {
		t.Fatalf("unmerged topic accepted: %v", err)
	}
	delete(s.m.Proposals["lib"], "isolated-topic")
	if err := os.WriteFile(filepath.Join(s.root, "lib", "new.txt"), []byte("new tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, s.root, "add", ".")
	mustGitStack(t, s.root, "commit", "-qm", "stale receipt")
	if err := s.checkTrack("lib"); err == nil || !strings.Contains(err.Error(), "tree differs") {
		t.Fatalf("stale tree accepted: %v", err)
	}
}

func TestStackEdgeFactsRepairRecheckAndSecondRun(t *testing.T) {
	s, f, out, _ := edgeSetupStackspace(t)
	f.environment, f.customBranches, f.policies = false, false, nil
	// Wiring is root work for the user to commit; edge commits only its workflow.
	if err := s.repairWiring("lib"); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, s.root, "add", ".")
	mustGitStack(t, s.root, "commit", "-qm", "root wiring")
	mustGitStack(t, s.root, "commit", "--allow-empty", "-qm", "stack: import app @ fixture")
	packs, prompts := 0, 0
	s.pack = func(context.Context, io.Writer, string, string) error { packs++; return nil }
	s.prompt = func(c *huh.Confirm) error { prompts++; return c.RunAccessible(out, strings.NewReader("y\n")) }
	if err := s.checkFacts([]string{"lib"}, true, false); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if packs != 1 || prompts != 8 || f.sets != 1 || f.writes != 4 {
		t.Fatalf("packs=%d prompts=%d sets=%d writes=%d\n%s", packs, prompts, f.sets, f.writes, out)
	}
	before := mustGitStack(t, s.root, "status", "--porcelain")
	s.prompt = func(*huh.Confirm) error { t.Fatal("second init prompted"); return nil }
	s.in = failEdgeSetupReader{t}
	if err := s.checkFacts([]string{"lib"}, true, false); err != nil {
		t.Fatal(err)
	}
	if packs != 2 || f.writes != 4 || f.sets != 1 {
		t.Fatal("second init performed a repair")
	}
	if after := mustGitStack(t, s.root, "status", "--porcelain"); before != after {
		t.Fatal("second init changed the checkout")
	}
}

func TestStackEdgeFactsDoctorChangesNothing(t *testing.T) {
	s, f, out, fork := edgeSetupStackspace(t)
	before := mustGitStack(t, s.root, "status", "--porcelain")
	refs := mustGitStack(t, s.root, "for-each-ref")
	remoteRefs := mustGitStack(t, fork, "for-each-ref")
	t.Setenv("GITHUB_API_URL", f.server.URL)
	oldPack, oldPrompt := stackEdgeSetupPack, stackEdgeSetupPrompt
	t.Cleanup(func() { stackEdgeSetupPack, stackEdgeSetupPrompt = oldPack, oldPrompt })
	packs := 0
	stackEdgeSetupPack = func(context.Context, io.Writer, string, string) error { packs++; return nil }
	stackEdgeSetupPrompt = func(*huh.Confirm) error { t.Fatal("doctor prompted"); return nil }
	cmd := newStackDoctorCmd()
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--edge"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("doctor accepted missing facts")
	}
	if packs != 1 || f.writes != 0 || f.sets != 0 {
		t.Fatalf("doctor writes=%d packs=%d", f.writes, packs)
	}
	if before != mustGitStack(t, s.root, "status", "--porcelain") || refs != mustGitStack(t, s.root, "for-each-ref") || remoteRefs != mustGitStack(t, fork, "for-each-ref") {
		t.Fatal("doctor changed git or checkout")
	}
	if !strings.Contains(out.String(), "packageSourceMapping") || !strings.Contains(out.String(), `pattern="You.*"`) {
		t.Fatalf("consumer mapping missing: %s", out)
	}
}

func TestStackEdgeFactsDryRunNoRepairsOrPack(t *testing.T) {
	s, f, out, _ := edgeSetupStackspace(t)
	s.pack = func(context.Context, io.Writer, string, string) error {
		t.Fatal("dry run built project code")
		return nil
	}
	s.prompt = func(*huh.Confirm) error { t.Fatal("dry run prompted"); return nil }
	before := mustGitStack(t, s.root, "status", "--porcelain")
	if err := s.checkFacts([]string{"lib"}, true, true); err == nil {
		t.Fatal("dry run concealed missing facts")
	}
	if f.writes != 0 || before != mustGitStack(t, s.root, "status", "--porcelain") {
		t.Fatal("dry run wrote")
	}
	for _, want := range []string{"(a)", "(b)", "(c)", "(d)", "(f)", "(g)", "(h)", "would reconstruct and build"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %s", want)
		}
	}
	if !strings.Contains(out.String(), "(e)") || !strings.Contains(out.String(), "(g-environment)") {
		t.Fatal("workflow or environment fact absent")
	}
}

func TestStackEdgeFactsCoverageAndWiring(t *testing.T) {
	s, _, _, _ := edgeSetupStackspace(t)
	s.m.Repos["lib"].PublishPrefix = ""
	s.m.Repos["lib"].PublishesAs = map[string]string{"NotLib": "You.NotLib"}
	if err := s.checkManifest("lib"); err == nil {
		t.Fatal("partial publishing coverage accepted")
	}
	s.m.Repos["lib"].PublishPrefix = "You."
	if err := s.checkManifest("lib"); err != nil {
		t.Fatal(err)
	}
	member := filepath.Join(s.root, "app", "Directory.Build.targets")
	original := []byte("<Project />\n")
	if err := os.WriteFile(member, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.checkWiring("lib"); err == nil {
		t.Fatal("missing overlay passed")
	}
	if err := s.repairWiring("lib"); err != nil {
		t.Fatal(err)
	}
	if err := s.checkWiring("lib"); err == nil {
		t.Fatal("hidden overlay passed")
	}
	if data, _ := os.ReadFile(member); !bytes.Equal(data, original) {
		t.Fatal("wire edited an owned member")
	}
	if err := os.Remove(member); err != nil {
		t.Fatal(err)
	}
	if err := s.checkWiring("lib"); err != nil {
		t.Fatal(err)
	}
	// A second producer claiming the published id suppresses that redirect.
	csproj(t, s.root, "app/Collision", "You.Lib")
	if err := s.checkWiring("lib"); err == nil || !strings.Contains(err.Error(), "no stackRedirects link") {
		t.Fatalf("unredirected alias passed: %v", err)
	}
}

func TestStackEdgeFactsPublishingDefaultsToHome(t *testing.T) {
	s, _, _, _ := edgeSetupStackspace(t)
	s.m.Edge["lib"].PublishFrom = ""
	mustGitStack(t, s.root, "config", "--unset", "rig.stackHomeURL")
	if _, err := s.publishingRepo("lib"); err == nil {
		t.Fatal("fell back to PR fork")
	}
	mustGitStack(t, s.root, "config", "rig.stackHomeURL", "https://github.com/publisher/edge.git")
	if slug, err := s.publishingRepo("lib"); err != nil || slug != "publisher/edge" {
		t.Fatalf("slug=%q err=%v", slug, err)
	}
	if err := s.checkPublishingRepo("lib"); err != nil {
		t.Fatal(err)
	}
}

func TestStackEdgeFactsDoctorDryRunDoesNotBuild(t *testing.T) {
	s, f, out, _ := edgeSetupStackspace(t)
	t.Setenv("GITHUB_API_URL", f.server.URL)
	oldPack, oldDryRun := stackEdgeSetupPack, dryRun
	t.Cleanup(func() { stackEdgeSetupPack, dryRun = oldPack, oldDryRun })
	dryRun = true
	stackEdgeSetupPack = func(context.Context, io.Writer, string, string) error {
		t.Fatal("doctor dry run built project code")
		return nil
	}
	cmd := newStackDoctorCmd()
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--edge"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("dry run concealed missing facts")
	}
	if !strings.Contains(out.String(), "would reconstruct and build") || f.writes != 0 {
		t.Fatalf("dry run output/writes: %s", out)
	}
	if err := s.checkManifest("lib"); err != nil {
		t.Fatal(err)
	}
}
