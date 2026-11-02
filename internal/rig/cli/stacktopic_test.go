package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
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
	stackTopicPreserveTokens(t)
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
		if r.Header.Get("Authorization") != "Bearer topic-api-fixture" {
			t.Error("API token missing")
		}
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
	t.Setenv("GITHUB_TOKEN", "topic-api-fixture")
	t.Setenv("GH_TOKEN", "topic-gh-fixture")
	t.Setenv("ACTIONS_RUNTIME_TOKEN", "topic-runtime-fixture")
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
	c.SetArgs([]string{"--topic", "topic", "--manifest", "rig.stack.jsonc", "--member", "app", "--allowed-hosts", srv.addr, "--build", "test -z \"${GITHUB_TOKEN}${GH_TOKEN}${ACTIONS_RUNTIME_TOKEN}\" && test -f topic-head && test -f ../lib/topic-head", "--test", "test -z \"${GITHUB_TOKEN}${GH_TOKEN}${ACTIONS_RUNTIME_TOKEN}\" && test -f topic-head && test -f ../lib/topic-head"})
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

	// The reviewed seed supplies policy independently of the PR checkout.
	seedDir := filepath.Join(work, "seed-source")
	mustGitStack(t, work, "init", seedDir)
	mustGitStack(t, seedDir, "config", "user.name", "test")
	mustGitStack(t, seedDir, "config", "user.email", "test@example.com")
	policy, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(seedDir, "rig.stack.jsonc"), policy, 0600)
	mustGitStack(t, seedDir, "add", ".")
	mustGitStack(t, seedDir, "commit", "-qm", "reviewed topic policy")
	pin := strings.TrimSpace(mustGitStack(t, seedDir, "rev-parse", "HEAD"))
	// A moving tip must not replace the pinned proposal set.
	os.WriteFile(filepath.Join(seedDir, "rig.stack.jsonc"), []byte("{}"), 0600)
	mustGitStack(t, seedDir, "commit", "-qam", "unreviewed policy")
	bare := filepath.Join(work, "policy.git")
	mustGitStack(t, work, "clone", "--bare", seedDir, bare)
	mustGitStack(t, checkout, "rm", "rig.stack.jsonc")
	mustGitStack(t, checkout, "commit", "-qm", "manifest lives in seed")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+stackTopicFileURL(bare)+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/acme/stack-policy.git")
	t.Setenv("GITHUB_TOKEN", "topic-api-fixture")
	t.Setenv("GH_TOKEN", "topic-gh-fixture")
	oldSeedGit := stackCISeedGit
	stackCISeedGit = func(ctx context.Context, dir string, args ...string) (string, error) {
		for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "ACTIONS_RUNTIME_TOKEN"} {
			if os.Getenv(name) != "" {
				return "", fmt.Errorf("seed child would inherit %s", name)
			}
		}
		return oldSeedGit(ctx, dir, args...)
	}
	t.Cleanup(func() { stackCISeedGit = oldSeedGit })
	c = newStackCICmd()
	c.SetArgs([]string{"--topic", "topic", "--seed", "acme/stack-policy@" + pin, "--member", "app", "--allowed-hosts", srv.addr + ",github.com", "--build", "test -z \"${GITHUB_TOKEN}${GH_TOKEN}\" && test -f topic-head && test -f ../lib/topic-head", "--test", "test -z \"${GITHUB_TOKEN}${GH_TOKEN}\" && test -f topic-head && test -f ../lib/topic-head"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(provenance)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	if records["_seed"]["commit"] != pin {
		t.Fatal("seed pin not respected", records)
	}
	for n, pr := range heads {
		if records[n]["commit"] != pr.Head.SHA {
			t.Fatal("seed topic head not applied", records)
		}
	}
}

func stackTopicFileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(path), "/")}).String()
}

func TestStackTopicPersistedCredentials(t *testing.T) {
	stackTopicPreserveTokens(t)
	dir := t.TempDir()
	mustGitStack(t, dir, "init", "-q")
	mustGitStack(t, dir, "config", "http.https://github.com/.extraheader", "AUTHORIZATION: bearer do-not-log-this")
	chdir(t, dir)
	c := newStackCICmd()
	c.SetArgs([]string{"--topic", "topic", "--manifest", "rig.stack.jsonc"})
	err := c.Execute()
	if err == nil || !strings.Contains(err.Error(), "persist-credentials: false") {
		t.Fatalf("expected persisted credential refusal: %v", err)
	}
	if strings.Contains(err.Error(), "do-not-log-this") {
		t.Fatal("credential leaked in refusal")
	}
}

func TestStackTopicAPIRemovesChildTokens(t *testing.T) {
	stackTopicPreserveTokens(t)
	names := []string{"GITHUB_TOKEN", "GH_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "ACTIONS_RUNTIME_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_TOKEN", "SYSTEM_ACCESSTOKEN", "AZURE_DEVOPS_EXT_PAT"}
	for _, name := range names {
		t.Setenv(name, "fixture-secret")
	}
	api, err := stackTopicAPI()
	if err != nil {
		t.Fatal(err)
	}
	if api.token != "fixture-secret" {
		t.Fatal("metadata client lost authentication")
	}
	for _, name := range names {
		if _, ok := os.LookupEnv(name); ok {
			t.Fatalf("%s would be inherited by a child", name)
		}
	}
}

func TestStackTopicWorkflow(t *testing.T) {
	data, err := os.ReadFile("../../../examples/github-workflows/stack-topic.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Permissions map[string]string
		Jobs        map[string]struct {
			Name  string
			Steps []struct {
				Uses string
				With map[string]string
			}
		}
	}
	if err = yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	if len(workflow.Permissions) != 2 || workflow.Permissions["contents"] != "read" || workflow.Permissions["pull-requests"] != "read" {
		t.Fatal("topic workflow requires read-only contents and pull requests")
	}
	job := workflow.Jobs["stack-topic"]
	if job.Name != "stack topic" {
		t.Fatal("missing shared check name")
	}
	checkout, policyCheckout, topic := false, false, false
	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, "actions/checkout@") {
			checkout = true
			if step.With["persist-credentials"] != "false" {
				t.Fatal("checkout must not persist credentials")
			}
			if step.With["repository"] == "acme/stack-policy" {
				policyCheckout = step.With["ref"] == "${{ vars.STACK_POLICY_SHA }}" && step.With["path"] == ".stack-policy"
			}
		}
		if step.Uses == "./.stack-policy/.github/actions/stack" {
			topic = step.With["topic"] != "" && step.With["member"] != "" && step.With["seed"] == "acme/stack-policy@${{ vars.STACK_POLICY_SHA }}" && step.With["manifest"] == ""
		}
	}
	if !checkout || !policyCheckout || !topic {
		t.Fatal("missing topic action or trusted checkout")
	}
}

func stackTopicPreserveTokens(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if strings.HasSuffix(upper, "_TOKEN") || strings.HasSuffix(upper, "_PAT") || upper == "SYSTEM_ACCESSTOKEN" {
			t.Setenv(name, value)
		}
	}
}

func TestStackTopicTokenTestIsolation(t *testing.T) {
	t.Setenv("NPM_TOKEN", "ambient-fixture")
	t.Run("scrub", func(t *testing.T) {
		stackTopicPreserveTokens(t)
		if _, err := stackTopicAPI(); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("NPM_TOKEN") != "" {
			t.Fatal("ambient token was not scrubbed")
		}
	})
	if os.Getenv("NPM_TOKEN") != "ambient-fixture" {
		t.Fatal("test leaked token removal to later tests")
	}
}
