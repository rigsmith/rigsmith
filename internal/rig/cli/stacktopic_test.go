package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStackTopicHeads(t *testing.T) {
	for _, mode := range []string{"open", "closed", "untrusted", "conflict", "missing", "fork-job", "target"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("GITHUB_EVENT_NAME", "pull_request")
			if mode == "target" {
				t.Setenv("GITHUB_EVENT_NAME", "pull_request_target")
			}
			if mode == "fork-job" {
				t.Setenv("STACK_FORK_PR", "true")
			}
			pr := stackDepsPR{Number: 1, State: "open"}
			pr.Head.Ref = "stack/topic"
			pr.Head.SHA = strings.Repeat("a", 40)
			pr.Head.Repo.FullName = "you/app"
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("topic resolver attempted a write")
				}
				detail := pr
				if !strings.HasSuffix(r.URL.Path, "/pulls") {
					switch mode {
					case "closed":
						detail.State = "closed"
					case "untrusted":
						detail.Head.Repo.FullName = "stranger/app"
					case "conflict":
						b := false
						detail.Mergeable = &b
					}
				}
				if strings.HasSuffix(r.URL.Path, "/pulls") {
					if mode == "missing" {
						json.NewEncoder(w).Encode([]stackDepsPR{})
						return
					}
					json.NewEncoder(w).Encode([]stackDepsPR{detail})
				} else {
					json.NewEncoder(w).Encode(detail)
				}
			}))
			defer s.Close()
			m := &stackManifest{Repos: map[string]*stackRepo{"app": {Upstream: "github.com/acme/app", Fork: "github.com/you/app"}}, Proposals: map[string]map[string]stackProposal{"app": {"topic": {Branch: "stack/topic"}}}}
			heads, err := stackTopicHeads(t.Context(), m, "topic", stackDepsAPI{s.URL, "", s.Client()})
			if mode == "open" || mode == "fork-job" {
				if err != nil || len(heads) != 1 {
					t.Fatal(heads, err)
				}
			} else if err == nil {
				t.Fatal("accepted", mode)
			}
		})
	}
}

// Both changed contracts must be present. Either isolated PR tree is broken.
func TestStackTopicTogether(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") == "" {
		t.Skip("set RIG_STACK_E2E=1")
	}
	work := t.TempDir()
	root := filepath.Join(work, "stack")
	repos := filepath.Join(work, "repos")
	os.MkdirAll(root, 0755)
	os.MkdirAll(repos, 0755)
	mustGitStack(t, root, "init", "-q")
	mustGitStack(t, root, "config", "user.name", "test")
	mustGitStack(t, root, "config", "user.email", "test@example.com")
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/topic\n\ngo 1.23\n"), 0644)
	base := map[string]string{
		"app": "package app\nimport (\"testing\"; \"example.test/topic/lib\")\nfunc TestContract(t *testing.T) { if lib.Version() != \"v1\" { t.Fatal(\"contract\") } }\n",
		"lib": "package lib\nfunc Version() string { return \"v1\" }\n",
	}
	changed := map[string]string{
		"app": "package app\nimport (\"testing\"; \"example.test/topic/lib\")\nfunc TestContract(t *testing.T) { if lib.Version(\"v2\") != \"v2\" { t.Fatal(\"contract\") } }\n",
		"lib": "package lib\nfunc Version(v string) string { return v }\n",
	}
	file := map[string]string{"app": "contract_test.go", "lib": "api.go"}
	heads := map[string]stackDepsPR{}
	m := &stackManifest{Repos: map[string]*stackRepo{}}
	for _, n := range []string{"app", "lib"} {
		source := filepath.Join(work, n)
		os.Mkdir(source, 0755)
		mustGitStack(t, source, "init", "-q")
		mustGitStack(t, source, "config", "user.name", "test")
		mustGitStack(t, source, "config", "user.email", "test@example.com")
		os.WriteFile(filepath.Join(source, file[n]), []byte(changed[n]), 0644)
		mustGitStack(t, source, "add", ".")
		mustGitStack(t, source, "commit", "-qm", "topic")
		sha := strings.TrimSpace(mustGitStack(t, source, "rev-parse", "HEAD"))
		mustGitStack(t, work, "clone", "--bare", source, filepath.Join(repos, n+".git"))
		pr := stackDepsPR{Number: 1, State: "open"}
		pr.Head.SHA = sha
		pr.Head.Repo.FullName = "you/" + n
		heads[n] = pr
		m.Repos[n] = &stackRepo{Upstream: "github.com/acme/" + n, Fork: "github.com/you/" + n}
		os.Mkdir(filepath.Join(root, n), 0755)
		os.WriteFile(filepath.Join(root, n, file[n]), []byte(base[n]), 0644)
	}
	mustGitStack(t, root, "add", ".")
	mustGitStack(t, root, "commit", "-qm", "cursor")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+stackTopicFileURL(repos)+"/.insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/you/")
	check := func() ([]byte, error) {
		p := exec.CommandContext(t.Context(), "go", "test", "./...")
		p.Dir = root
		p.Env = append(os.Environ(), "GOWORK=off")
		return p.CombinedOutput()
	}

	for _, n := range []string{"app", "lib"} {
		if err := stackTopicApply(t.Context(), root, m, map[string]stackDepsPR{n: heads[n]}, map[string]string{}); err != nil {
			t.Fatal(err)
		}
		if out, err := check(); err == nil || (!strings.Contains(string(out), "not enough arguments") && !strings.Contains(string(out), "too many arguments")) {
			t.Fatalf("isolated %s must fail compilation on the sibling API: %v\n%s", n, err, out)
		}
		os.WriteFile(filepath.Join(root, n, file[n]), []byte(base[n]), 0644)
	}
	resolved := map[string]string{}
	if err := stackTopicApply(t.Context(), root, m, heads, resolved); err != nil {
		t.Fatal(err)
	}
	if out, err := check(); err != nil || len(resolved) != 2 {
		t.Fatalf("combined topic failed: %v, %v\n%s", resolved, err, out)
	}
}

func TestStackTopicCIFlow(t *testing.T) {
	if os.Getenv("RIG_STACK_E2E") == "" {
		t.Skip("set RIG_STACK_E2E=1")
	}
	bin, err := stackJoshProxyBin(stackJoshVersion)
	if err != nil || stackJoshInstalled(bin) != nil {
		t.Fatal("install pinned josh before running E2E")
	}
	work := t.TempDir()
	srv := newGitServer(t, filepath.Join(work, "srv"))
	srv.seed(t, "acme/app", "app")
	srv.seed(t, "acme/lib", "lib")
	m := &stackManifest{Repos: map[string]*stackRepo{}, LastSync: map[string]string{}, Proposals: map[string]map[string]stackProposal{}}
	heads := map[string]stackDepsPR{}
	for _, n := range []string{"app", "lib"} {
		dir := filepath.Join(work, n)
		mustGitStack(t, work, "clone", stackTopicFileURL(srv.path("acme/"+n)), dir)
		mustGitStack(t, dir, "config", "user.name", "test")
		mustGitStack(t, dir, "config", "user.email", "test@example.com")
		m.LastSync[n] = strings.TrimSpace(mustGitStack(t, dir, "rev-parse", "HEAD"))
		os.WriteFile(filepath.Join(dir, "topic-head"), []byte(n), 0644)
		mustGitStack(t, dir, "add", ".")
		mustGitStack(t, dir, "commit", "-qm", "topic")
		mustGitStack(t, dir, "push", "origin", "HEAD:refs/heads/stack/topic")
		pr := stackDepsPR{Number: 1, State: "open"}
		pr.Head.SHA = strings.TrimSpace(mustGitStack(t, dir, "rev-parse", "HEAD"))
		pr.Head.Ref = "stack/topic"
		pr.Head.Repo.FullName = "acme/" + n
		heads[n] = pr
		m.Repos[n] = &stackRepo{Upstream: srv.spec("acme/" + n), Fork: srv.spec("acme/" + n)}
		m.Proposals[n] = map[string]stackProposal{"topic": {Branch: "stack/topic"}}
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := "app"
		if strings.Contains(r.URL.Path, "/lib/") {
			n = "lib"
		}
		if strings.HasSuffix(r.URL.Path, "/pulls") {
			json.NewEncoder(w).Encode([]stackDepsPR{heads[n]})
		} else {
			json.NewEncoder(w).Encode(heads[n])
		}
	}))
	defer api.Close()
	t.Setenv("GITHUB_EVENT_NAME", "pull_request")
	t.Setenv("STACK_FORK_PR", "true")
	t.Setenv("GITHUB_API_URL", api.URL)
	t.Setenv("GITHUB_TOKEN", "")
	checkout := filepath.Join(work, "app")
	data, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(checkout, "rig.stack.jsonc"), data, 0644)
	mustGitStack(t, checkout, "add", ".")
	mustGitStack(t, checkout, "commit", "-qm", "manifest")
	exe := filepath.Join(work, "rig")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	p := exec.Command("go", "build", "-o", exe, "../../../cmd/rig")
	if out, err := p.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	old := stackCIExecutable
	stackCIExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { stackCIExecutable = old })
	chdir(t, checkout)
	provenance := filepath.Join(work, "provenance.json")
	t.Setenv("RIG_STACK_PROVENANCE", provenance)
	c := newStackCICmd()
	c.SetArgs([]string{"--topic", "topic", "--manifest", "rig.stack.jsonc", "--member", "app", "--allowed-hosts", srv.addr, "--build", "test -f topic-head && test -f ../lib/topic-head", "--test", "test -f topic-head && test -f ../lib/topic-head"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(provenance)
	if err != nil {
		t.Fatal(err)
	}
	var records map[string]map[string]string
	if err = json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	for n, pr := range heads {
		if records[n]["commit"] != pr.Head.SHA || records[n]["source"] != "topic-head" || records[n]["headRepository"] != pr.Head.Repo.FullName {
			t.Fatal(records)
		}
	}
}

func stackTopicFileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(path), "/")}).String()
}
