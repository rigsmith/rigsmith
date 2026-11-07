package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

func edgeWorkflowFixture(t *testing.T) *stackEdgeSetup {
	t.Helper()
	s, _, _ := edgeSetupFixture(t)
	mustGitStack(t, s.root, "init", "-b", "main")
	mustGitStack(t, s.root, "config", "user.name", "test")
	mustGitStack(t, s.root, "config", "user.email", "test@example.invalid")
	data, _ := json.Marshal(s.m)
	writeStackManifest(t, s.root, string(data))
	mustGitStack(t, s.root, "add", "rig.stack.jsonc")
	mustGitStack(t, s.root, "commit", "-qm", "manifest")
	home := filepath.Join(t.TempDir(), "home.git")
	mustGitStack(t, s.root, "init", "--bare", "-b", "main", home)
	if err := stackConfigureHome(s.ctx, s.root, home, "main", ""); err != nil {
		t.Fatal(err)
	}
	t.Chdir(s.root)
	return s
}

func TestStackEdgeWorkflowTemplateCopy(t *testing.T) {
	data, err := os.ReadFile("../../../examples/github-workflows/stack-edge.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte(stackEdgeWorkflowTemplate)) {
		t.Fatal("embedded template differs from example")
	}
}

func TestStackEdgeWorkflowMembersAndSecretNames(t *testing.T) {
	s := edgeWorkflowFixture(t)
	s.m.Edge["lib"].PushKeySecret = "LIB_PUSH_KEY"
	s.m.Repos["other"] = s.m.Repos["lib"]
	s.m.Edge["other"] = &stackEdge{Feed: "https://example.invalid/index.json", PushKeySecret: "OTHER_PUSH_KEY"}
	first, err := s.workflow("lib")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.workflow("other")
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("members disagree on root workflow")
	}
	var workflow struct {
		Jobs map[string]struct {
			Needs, Environment string
			Steps              []struct {
				Run string
				Env map[string]string
			}
		}
	}
	if err := yaml.Unmarshal(first, &workflow); err != nil {
		t.Fatal(err)
	}
	if len(workflow.Jobs) != 4 || workflow.Jobs["publish_0"].Needs != "build_0" || workflow.Jobs["publish_1"].Needs != "build_1" {
		t.Fatalf("job pairs: %s", first)
	}
	for name, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if strings.Contains(step.Run, "--publish") {
				secret := "LIB_PUSH_KEY"
				member := "lib"
				if name == "publish_1" {
					secret, member = "OTHER_PUSH_KEY", "other"
				}
				if job.Environment != "edge-publish" || step.Env[secret] != "${{ secrets."+secret+" }}" || !strings.Contains(step.Run, "--member '"+member+"'") {
					t.Fatalf("publisher %s: %+v", name, step)
				}
			}
		}
	}
}

func TestStackEdgeWorkflowPublication(t *testing.T) {
	s := edgeWorkflowFixture(t)
	if err := s.checkWorkflowPublication("lib"); err == nil {
		t.Fatal("missing workflow passed")
	}
	if err := s.repairWorkflowPublication("lib"); err != nil {
		t.Fatal(err)
	}
	if err := s.checkWorkflowPublication("lib"); err != nil {
		t.Fatal(err)
	}
	want, err := s.workflow("lib")
	if err != nil {
		t.Fatal(err)
	}
	assertStackEdgeFile(t, s.root, stackEdgeWorkflowPath, string(want))
	if files := strings.TrimSpace(mustGitStack(t, s.root, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD")); files != stackEdgeWorkflowPath {
		t.Fatalf("commit paths: %q", files)
	}
	before := mustGitStack(t, s.root, "rev-parse", "HEAD")
	s.prompt = func(*huh.Confirm) error { t.Fatal("second init prompted"); return nil }
	if err := s.repairWorkflowPublication("lib"); err != nil {
		t.Fatal(err)
	}
	if before != mustGitStack(t, s.root, "rev-parse", "HEAD") {
		t.Fatal("second init committed")
	}
	if _, err := os.Stat(filepath.Join(s.root, "lib", stackEdgeWorkflowPath)); !os.IsNotExist(err) {
		t.Fatal("wrote inside member")
	}
	// Published content is checked independently of the local file and receipt.
	home := stackHomeConfig(s.ctx, s.root, "URL")
	mustGitStack(t, home, "update-ref", "-d", "refs/heads/main")
	if err := s.checkWorkflowPublication("lib"); err == nil {
		t.Fatal("absent published workflow passed")
	}
}

func TestStackEdgeWorkflowNonTTY(t *testing.T) {
	for _, stage := range []string{"write", "commit", "publish"} {
		t.Run(stage, func(t *testing.T) {
			s := edgeWorkflowFixture(t)
			want, err := s.workflow("lib")
			if err != nil {
				t.Fatal(err)
			}
			if stage != "write" {
				path := filepath.Join(s.root, filepath.FromSlash(stackEdgeWorkflowPath))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, want, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "publish" {
				mustGitStack(t, s.root, "add", stackEdgeWorkflowPath)
				mustGitStack(t, s.root, "commit", "-qm", "workflow")
			}
			before := mustGitStack(t, s.root, "status", "--porcelain")
			head := mustGitStack(t, s.root, "rev-parse", "HEAD")
			s.tty = func() bool { return false }
			s.prompt = func(*huh.Confirm) error { t.Fatal("prompt reached without terminal"); return nil }
			err = s.repairWorkflowPublication("lib")
			if err == nil || !strings.Contains(err.Error(), "stdin and stdout on a terminal") {
				t.Fatalf("non-TTY: %v", err)
			}
			if before != mustGitStack(t, s.root, "status", "--porcelain") || head != mustGitStack(t, s.root, "rev-parse", "HEAD") {
				t.Fatal("non-TTY changed checkout")
			}
			if refs := mustGitStack(t, stackHomeConfig(s.ctx, s.root, "URL"), "for-each-ref"); refs != "" {
				t.Fatal("non-TTY published")
			}
		})
	}
}

func TestStackEdgeWorkflowCommitOnlyPath(t *testing.T) {
	s := edgeWorkflowFixture(t)
	if err := os.WriteFile(filepath.Join(s.root, "unrelated.txt"), []byte("unfinished"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, s.root, "add", "unrelated.txt")
	if err := s.repairWorkflowPublication("lib"); err == nil || !strings.Contains(err.Error(), "commit stack changes") {
		t.Fatalf("dirty publication: %v", err)
	}
	if files := strings.TrimSpace(mustGitStack(t, s.root, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD")); files != stackEdgeWorkflowPath {
		t.Fatalf("committed unrelated work: %q", files)
	}
	if staged := strings.TrimSpace(mustGitStack(t, s.root, "diff", "--cached", "--name-only")); staged != "unrelated.txt" {
		t.Fatalf("unrelated stage: %q", staged)
	}
}

func TestStackEdgeWorkflowNeverInsideMember(t *testing.T) {
	s := edgeWorkflowFixture(t)
	member := filepath.Join(s.root, "lib")
	if err := os.MkdirAll(member, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(member, filepath.Join(s.root, ".github")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := s.repairWorkflowPublication("lib"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("member symlink: %v", err)
	}
	if entries, _ := os.ReadDir(member); len(entries) != 0 {
		t.Fatal("member changed")
	}
}

func TestStackEdgeMemberPromptAbort(t *testing.T) {
	_ = inTempStackspace(t, `{"repos":{"lib":{"upstream":"github.com/acme/Lib","fork":"github.com/you/Lib","trackBranch":"stack/integration","publishPrefix":"You."}},"edge":{"lib":{"feed":"https://example.invalid/index.json"}}}`)
	old := stackEdgeMemberPrompt
	t.Cleanup(func() { stackEdgeMemberPrompt = old })
	for _, failure := range []error{huh.ErrUserAborted, errors.New("prompt broke")} {
		stackEdgeMemberPrompt = func(*huh.Form) error { return failure }
		for _, cmd := range []*cobra.Command{newStackEdgePackCmd(), newStackEdgeAdoptCmd()} {
			cmd.RunE = func(*cobra.Command, []string) error { t.Fatal("cancel ran command"); return nil }
			menu := newStackEdgeMemberMenuCmd(cmd)
			menu.SetContext(t.Context())
			err := menu.RunE(menu, nil)
			if failure == huh.ErrUserAborted && err != nil || failure != huh.ErrUserAborted && err != failure {
				t.Fatalf("cancel result: %v", err)
			}
		}
	}
}
