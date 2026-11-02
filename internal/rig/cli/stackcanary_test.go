package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"go.yaml.in/yaml/v3"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestStackCanaryPublish(t *testing.T) {
	for _, green := range []bool{true, false} {
		t.Run(map[bool]string{true: "green", false: "red"}[green], func(t *testing.T) {
			creates, updates := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				p := r.URL.Path
				if r.Method == "GET" {
					switch {
					case strings.HasSuffix(p, "/issues"):
						if !green && creates > 0 {
							json.NewEncoder(w).Encode([]any{map[string]any{"number": 1, "body": "<!-- rig-canary:" + canaryTestKey() + " -->"}})
						} else {
							w.Write([]byte("[]"))
						}
					case strings.HasSuffix(p, "/pulls"):
						if green && creates > 0 {
							w.Write([]byte(`[{"number":1}]`))
						} else {
							w.Write([]byte("[]"))
						}
					default:
						w.Write([]byte(`{"sha":"abc","tree":{"sha":"tree"},"object":{"sha":"abc"}}`))
					}
					return
				}
				if strings.HasSuffix(p, "/pulls") || strings.HasSuffix(p, "/pulls/1") {
					var payload struct{ Body string }
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if !strings.Contains(payload.Body, "reported green by the build job") || !strings.Contains(payload.Body, "not the build outcome") {
						t.Error("PR body overstates verification")
					}
				}
				if strings.HasSuffix(p, "/git/trees") {
					var payload struct {
						BaseTree string `json:"base_tree"`
						Tree     []struct{ Path, Content string }
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if payload.BaseTree != "tree" || len(payload.Tree) != 1 || payload.Tree[0].Path != "rig.stack.jsonc" {
						t.Error("not a manifest-only tree")
					}
				}
				if strings.HasSuffix(p, "/git/commits") {
					var payload struct{ Parents []string }
					json.NewDecoder(r.Body).Decode(&payload)
					if len(payload.Parents) != 1 || payload.Parents[0] != strings.Repeat("a", 40) {
						t.Error("commit must have only seed parent")
					}
				}
				if strings.HasSuffix(p, "/issues") || strings.HasSuffix(p, "/pulls") {
					creates++
				}
				if strings.HasSuffix(p, "/issues/1") || strings.HasSuffix(p, "/pulls/1") {
					updates++
				}
				w.Write([]byte(`{"sha":"abc"}`))
			}))
			defer srv.Close()
			a := stackDepsAPI{srv.URL, "secret", &http.Client{Timeout: time.Second}}
			r := stackCanaryResult{Changes: []stackCanaryChange{{"app", strings.Repeat("a", 40), strings.Repeat("b", 40), []string{strings.Repeat("b", 40)}}}, Green: green}
			for i := 0; i < 2; i++ {
				if err := stackCanaryPublish(context.Background(), a, "acme/home", "main", strings.Repeat("a", 40), []byte(`{}`), r); err != nil {
					t.Fatal(err)
				}
			}
			if creates != 1 || updates != 1 {
				t.Fatalf("creates=%d updates=%d", creates, updates)
			}
		})
	}
}
func canaryTestKey() string { return "a172cedcae47474b" }
func TestStackCanaryEnv(t *testing.T) {
	t.Setenv("CANARY_TOKEN", "secret")
	t.Setenv("CANARY_PAT", "secret")
	for _, v := range stackCanaryEnv() {
		if strings.Contains(v, "CANARY_TOKEN=") || strings.Contains(v, "CANARY_PAT=") {
			t.Fatal("token leaked")
		}
	}
}
func TestStackCanaryText(t *testing.T) {
	s := stackCanaryText("@you\n::set-output [`x`]")
	if strings.ContainsAny(s, "@\n`:") {
		t.Fatal(s)
	}
}
func TestStackCanaryWorkflow(t *testing.T) {
	b, e := os.ReadFile("../../../examples/github-workflows/stack-canary.yml")
	if e != nil {
		t.Fatal(e)
	}
	var workflow struct {
		Jobs map[string]struct {
			If          string
			Permissions map[string]string
			Steps       []struct {
				With map[string]any
				Uses string
			}
		}
	}
	if e = yaml.Unmarshal(b, &workflow); e != nil {
		t.Fatal(e)
	}
	if workflow.Jobs["publish"].If != "${{ needs.build.result == 'success' }}" {
		t.Fatal("publisher must require successful build job")
	}
	build := workflow.Jobs["build"]
	for _, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "actions/upload-artifact@") && step.Uses != "actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9" {
				t.Fatal("upload pin drift")
			}
			if strings.HasPrefix(step.Uses, "actions/download-artifact@") && step.Uses != "actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333" {
				t.Fatal("download pin drift")
			}
		}
	}
	for _, v := range build.Permissions {
		if v == "write" {
			t.Fatal("unsafe build permissions")
		}
	}
	if build.Permissions["contents"] != "read" {
		t.Fatal("missing read permissions")
	}
	if len(build.Steps) == 0 || build.Steps[0].With["persist-credentials"] != false {
		t.Fatal("checkout retains credentials")
	}
}

func TestStackCanaryLocalPreviewAndNoChanges(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		p := exec.Command("git", args...)
		p.Dir = dir
		p.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@localhost", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@localhost")
		b, e := p.CombinedOutput()
		if e != nil {
			t.Fatalf("git: %v %s", e, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "-q")
	manifest := `{"repos":{"app":{"upstream":"github.com/acme/app","fork":"github.com/you/app","upstreamCommit":"` + strings.Repeat("a", 40) + `"}},"lastSync":{"app":"` + strings.Repeat("a", 40) + `"}}`
	if e := os.WriteFile(filepath.Join(dir, "rig.stack.jsonc"), []byte(manifest), 0600); e != nil {
		t.Fatal(e)
	}
	git("add", ".")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "seed")
	bare := filepath.Join(t.TempDir(), "home.git")
	git("clone", "--bare", dir, bare)
	output := filepath.Join(t.TempDir(), "report.json")
	old := dryRun
	defer func() { dryRun = old }()
	dryRun = true
	c := newStackCanaryCmd()
	c.SetArgs([]string{"--seed", bare, "--report", output})
	if e := c.Execute(); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(output); !os.IsNotExist(e) {
		t.Fatal("preview wrote report")
	}
	dryRun = false
	c = newStackCanaryCmd()
	c.SetArgs([]string{"--seed", bare, "--report", output})
	if e := c.Execute(); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(output)
	if e != nil {
		t.Fatal(e)
	}
	var r stackCanaryReport
	if e = json.Unmarshal(b, &r); e != nil || len(r.Results) != 0 {
		t.Fatalf("%s %v", b, e)
	}
	dryRun = true
	c = newStackCanaryCmd()
	c.SetArgs([]string{"--seed", bare, "--publish", output})
	if e = c.Execute(); e != nil {
		t.Fatal(e)
	}
}

func TestStackCanaryChildToken(t *testing.T) {
	t.Setenv("CANARY_TOKEN", "secret")
	t.Setenv("CANARY_PAT", "secret")
	p := exec.Command("bash", "-c", `test -z "${CANARY_TOKEN:-}${CANARY_PAT:-}"`)
	p.Env = stackCanaryEnv()
	if e := p.Run(); e != nil {
		t.Fatal(e)
	}
}

func TestStackCanaryFlow(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") == "" {
		t.Skip("set RIG_STACK_E2E=1")
	}
	bin, e := stackJoshProxyBin(stackJoshVersion)
	if e != nil || stackJoshInstalled(bin) != nil {
		t.Skip("install pinned josh before E2E")
	}
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "acme/app", "app")
	b, e := stackCIGit(context.Background(), srv.path("acme/app"), "rev-parse", "main")
	if e != nil {
		t.Fatal(e)
	}
	from := strings.TrimSpace(string(b))
	source := filepath.Join(work, "source")
	mustGitStack(t, work, "clone", srv.path("acme/app"), source)
	mustGitStack(t, source, "config", "user.name", "test")
	mustGitStack(t, source, "config", "user.email", "test@localhost")
	if e = os.WriteFile(filepath.Join(source, "new-tip"), []byte("tip"), 0600); e != nil {
		t.Fatal(e)
	}
	mustGitStack(t, source, "add", ".")
	mustGitStack(t, source, "commit", "-qm", "upstream movement")
	mustGitStack(t, source, "push", "origin", "main")
	home := filepath.Join(work, "home")
	mustGitStack(t, work, "init", home)
	mustGitStack(t, home, "config", "user.name", "test")
	mustGitStack(t, home, "config", "user.email", "test@localhost")
	writeStackManifest(t, home, fmt.Sprintf(`{"repos":{"app":{"upstream":%q,"fork":%q}},"lastSync":{"app":%q}}`, srv.spec("acme/app"), srv.spec("acme/app"), from))
	mustGitStack(t, home, "add", ".")
	mustGitStack(t, home, "commit", "-qm", "seed")
	bare := filepath.Join(work, "home.git")
	mustGitStack(t, work, "clone", "--bare", home, bare)
	exe := filepath.Join(work, "rig")
	p := exec.Command("go", "build", "-o", exe, "../../../cmd/rig")
	if b, e := p.CombinedOutput(); e != nil {
		t.Fatalf("%s %v", b, e)
	}
	old := stackCIExecutable
	stackCIExecutable = func() (string, error) { return exe, nil }
	defer func() { stackCIExecutable = old }()
	t.Setenv("GITHUB_TOKEN", "never-visible")
	t.Setenv("CANARY_PAT", "never-visible")
	for _, green := range []bool{true, false} {
		test := "test -f app/new-tip && test -z \"${GITHUB_TOKEN:-}${CANARY_PAT:-}\""
		if !green {
			test = "exit 1"
		}
		report := filepath.Join(work, "report.json")
		c := newStackCanaryCmd()
		c.SetArgs([]string{"--seed", bare, "--allowed-hosts", srv.addr, "--build", "test -f app/new-tip", "--test", test, "--report", report})
		if e = c.Execute(); e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(report)
		if e != nil {
			t.Fatal(e)
		}
		var r stackCanaryReport
		if e = json.Unmarshal(b, &r); e != nil || len(r.Results) != 1 || r.Results[0].Green != green {
			t.Fatalf("report %s error %v", b, e)
		}
	}
}

func TestStackCanaryRejectsForkNetworkCommit(t *testing.T) {
	dir := t.TempDir()
	mustGitStack(t, dir, "init", "-q", "-b", "main")
	mustGitStack(t, dir, "config", "user.name", "test")
	mustGitStack(t, dir, "config", "user.email", "test@localhost")
	from, to := strings.Repeat("a", 40), strings.Repeat("b", 40)
	writeStackManifest(t, dir, fmt.Sprintf(`{"repos":{"app":{"upstream":"github.com/acme/app","fork":"github.com/you/app","upstreamBranch":"release/trusted"}},"lastSync":{"app":%q}}`, from))
	mustGitStack(t, dir, "add", ".")
	mustGitStack(t, dir, "commit", "-qm", "seed")
	base := strings.TrimSpace(mustGitStack(t, dir, "rev-parse", "HEAD"))
	bare := filepath.Join(t.TempDir(), "home.git")
	mustGitStack(t, dir, "clone", "--bare", dir, bare)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+filepath.ToSlash(bare)+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/acme/home.git")
	for _, status := range []string{"diverged", "ahead", "behind", "identical"} {
		t.Run(status, func(t *testing.T) {
			writes, checked := 0, false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes++
					w.Write([]byte(`{"sha":"new"}`))
					return
				}
				switch {
				case strings.Contains(r.URL.Path, "/compare/"+from+"..."+to):
					json.NewEncoder(w).Encode(map[string]any{"status": "ahead", "commits": []any{map[string]string{"sha": to}}})
				case strings.Contains(r.URL.Path, "/compare/refs/heads/"):
					checked = true
					if !strings.HasSuffix(r.URL.Path, "/compare/refs/heads/release/trusted..."+to) {
						t.Errorf("branch not read from manifest: %s", r.URL.Path)
					}
					json.NewEncoder(w).Encode(map[string]string{"status": status})
				case strings.HasSuffix(r.URL.Path, "/issues"), strings.HasSuffix(r.URL.Path, "/pulls"):
					w.Write([]byte(`[]`))
				default:
					w.Write([]byte(`{"sha":"seed","tree":{"sha":"tree"}}`))
				}
			}))
			defer srv.Close()
			t.Setenv("GITHUB_API_URL", srv.URL)
			t.Setenv("GITHUB_TOKEN", "test-token")
			report := filepath.Join(t.TempDir(), "report.json")
			b, e := json.Marshal(stackCanaryReport{Base: base, Results: []stackCanaryResult{{Changes: []stackCanaryChange{{Member: "app", From: from, To: to}}, Green: true}}})
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(report, b, 0600); e != nil {
				t.Fatal(e)
			}
			c := newStackCanaryCmd()
			c.SetArgs([]string{"--seed", "acme/home@main", "--publish", report})
			err := c.Execute()
			allowed := status == "behind" || status == "identical"
			if allowed {
				if err != nil || writes == 0 {
					t.Fatalf("allowed ancestor rejected: %v writes %d", err, writes)
				}
			} else if err == nil || writes != 0 {
				t.Fatalf("fork-network commit published: err=%v writes=%d", err, writes)
			}
			if !checked {
				t.Fatal("tracked upstream branch was not checked")
			}
		})
	}
}

func TestStackCanaryHelpAlignment(t *testing.T) {
	columns := regexp.MustCompile(`^  rig stack .*? {2,}`)
	column := -1
	for _, line := range strings.Split(newStackCmd().Long, "\n") {
		if !strings.HasPrefix(line, "  rig stack ") {
			continue
		}
		prefix := columns.FindString(line)
		if prefix == "" {
			t.Fatalf("missing description column: %s", line)
		}
		if column < 0 {
			column = len(prefix)
		}
		if len(prefix) != column {
			t.Fatalf("misaligned description: %s", line)
		}
	}
	if column < 0 {
		t.Fatal("no stack help entries")
	}
}
