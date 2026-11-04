package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type releasedPublishFixture struct {
	failMethod, failPath, prBase, headTree, headParent          string
	failStatus, commits                                         int
	sibling, pkg, via, deliveryMinimum                          string
	omitPair                                                    bool
	t                                                           *testing.T
	feed, report, app, appBare                                  string
	status                                                      string
	denied                                                      bool
	ref, pr                                                     bool
	opens, updates, refCreates, refUpdates, comparisons, writes int
	tree                                                        map[string]string
	body, title                                                 string
}

func newReleasedPublishFixture(t *testing.T) *releasedPublishFixture {
	t.Helper()
	f := &releasedPublishFixture{t: t, status: "ahead", sibling: "lib", pkg: "Acme.Lib", deliveryMinimum: "2.0.0", prBase: "main", tree: map[string]string{}}
	root := t.TempDir()
	cursor := strings.Repeat("a", 40)
	home := filepath.Join(root, "home")
	lib := filepath.Join(root, "lib")
	f.app = filepath.Join(root, "app")
	for _, dir := range []string{home, lib, f.app} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		mustGitStack(t, dir, "init", "-q", "-b", "main")
		mustGitStack(t, dir, "config", "user.name", "test")
		mustGitStack(t, dir, "config", "user.email", "test@localhost")
	}
	write := func(dir, path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(home, "rig.stack.jsonc", fmt.Sprintf(`{"repos":{"app":{"upstream":"github.com/acme/app","fork":"github.com/you/app"},"lib":{"upstream":"github.com/acme/lib","fork":"github.com/you/lib"}},"lastSync":{"app":%q,"lib":%q},"proposals":{"lib":{"topic":{"branch":"stack/topic"}}}}`, cursor, cursor))
	write(lib, "Lib.csproj", `<Project><PropertyGroup><PackageId>Acme.Lib</PackageId><Version>1.0.0</Version></PropertyGroup></Project>`)
	write(f.app, "App.csproj", `<Project><PropertyGroup><PackageId>Acme.App</PackageId><Version>1.0.0</Version></PropertyGroup><ItemGroup><PackageReference Include="Acme.Lib" Version="1.0.0" /><StackOnlyWarning Include="ACME0001" /></ItemGroup></Project>`)
	write(f.app, "Code.cs", "#if STACK_ACME_LIB\nApi.New();\n#else\nApi.Old();\n#endif\n")
	write(f.app, "Complex.cs", "#if STACK_ACME_LIB && OTHER\nApi.New();\n#endif\n")
	// These would execute if the privileged job evaluated MSBuild or ran hooks.
	write(f.app, ".rig.json", `{"build":"exit 99"}`)
	for i, entry := range []struct{ name, dir string }{{"home", home}, {"lib", lib}, {"app", f.app}} {
		mustGitStack(t, entry.dir, "add", ".")
		mustGitStack(t, entry.dir, "commit", "-qm", "fixture")
		bare := filepath.Join(root, entry.name+".git")
		mustGitStack(t, entry.dir, "clone", "--bare", entry.dir, bare)
		t.Setenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", i), "url."+filepath.ToSlash(bare)+".insteadOf")
		t.Setenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", i), "https://github.com/acme/"+entry.name+".git")
		if entry.name == "app" {
			f.appBare = bare
		}
	}
	t.Setenv("GIT_CONFIG_COUNT", "3")
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.feed = srv.URL
	f.report = filepath.Join(root, "released.json")
	t.Setenv("GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_TOKEN", "test")
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	return f
}
func (f *releasedPublishFixture) serve(w http.ResponseWriter, q *http.Request) {
	p := q.URL.Path
	if q.Method != "GET" {
		f.writes++
	}
	if q.Method == f.failMethod && p == f.failPath {
		w.WriteHeader(f.failStatus)
		return
	}
	switch {
	case strings.HasSuffix(p, "/index.json"):
		fmt.Fprint(w, `{"versions":["2.0.0"]}`)
	case strings.HasSuffix(p, ".nuspec"):
		if strings.Contains(p, "/acme.core/") {
			fmt.Fprintf(w, `<package><metadata><repository url="https://github.com/acme/core" commit="%s" /></metadata></package>`, strings.Repeat("b", 40))
			return
		}
		if f.via != "" {
			fmt.Fprintf(w, `<package><metadata><dependencies><dependency id="Acme.Core" version="%s" /></dependencies></metadata></package>`, f.deliveryMinimum)
			return
		}
		fmt.Fprintf(w, `<package><metadata><repository url="https://github.com/acme/lib" commit="%s" /></metadata></package>`, strings.Repeat("b", 40))
	case strings.Contains(p, "/compare/"):
		f.comparisons++
		fmt.Fprintf(w, `{"status":%q}`, f.status)
	case p == "/repos/acme/lib/pulls/1" || p == "/repos/acme/core/pulls/1":
		fmt.Fprintf(w, `{"merged_at":"today","merge_commit_sha":%q}`, strings.Repeat("a", 40))
	case p == "/repos/acme/lib/pulls" || p == "/repos/acme/core/pulls":
		fmt.Fprint(w, fmt.Sprintf(`[{"number":1,"head":{"ref":"stack/topic","repo":{"full_name":"you/%s"}}}]`, f.sibling))
	case p == "/repos/acme/app" || p == "/repos/acme/lib" || p == "/repos/acme/core":
		fmt.Fprint(w, `{"default_branch":"main"}`)
	case p == "/repos/acme/app/pulls" && q.Method == "GET":
		if q.URL.Query().Get("head") != "acme:rig-released/"+f.sibling {
			f.t.Errorf("unexpected head: %s", q.URL.RawQuery)
		}
		if f.pr {
			json.NewEncoder(w).Encode([]any{map[string]any{"number": 7, "base": map[string]string{"ref": f.prBase}, "title": f.title, "body": f.body}})
		} else {
			fmt.Fprint(w, `[]`)
		}
	case strings.Contains(p, "/git/commits/"):
		if strings.HasSuffix(p, "/new-commit") {
			json.NewEncoder(w).Encode(map[string]any{"tree": map[string]string{"sha": f.headTree}, "parents": []any{map[string]string{"sha": f.headParent}}})
			return
		}
		fmt.Fprint(w, `{"tree":{"sha":"base-tree"}}`)
	case strings.HasSuffix(p, "/git/trees"):
		var payload struct {
			BaseTree string `json:"base_tree"`
			Tree     []struct{ Path, Content, Mode, Type string }
		}
		if err := json.NewDecoder(q.Body).Decode(&payload); err != nil {
			f.t.Error(err)
		}
		if payload.BaseTree != "base-tree" {
			f.t.Error("not based on default tree")
		}
		f.tree = map[string]string{}
		for _, e := range payload.Tree {
			f.tree[e.Path] = e.Content
			if e.Mode != "100644" || e.Type != "blob" {
				f.t.Errorf("invalid entry %+v", e)
			}
		}
		fmt.Fprint(w, `{"sha":"new-tree"}`)
	case strings.HasSuffix(p, "/git/commits"):
		var payload struct {
			Tree    string
			Parents []string
		}
		if err := json.NewDecoder(q.Body).Decode(&payload); err != nil {
			f.t.Error(err)
		}
		if len(payload.Parents) != 1 {
			f.t.Fatalf("invalid parents: %v", payload.Parents)
		}
		f.commits++
		f.headTree, f.headParent = payload.Tree, payload.Parents[0]
		fmt.Fprint(w, `{"sha":"new-commit"}`)
	case strings.Contains(p, "/git/matching-refs/"):
		if !strings.HasSuffix(p, "/heads/rig-released/"+f.sibling) {
			f.t.Errorf("truncated ref lookup: %s", q.URL.String())
		}
		if f.ref {
			json.NewEncoder(w).Encode([]any{map[string]any{"ref": "refs/heads/rig-released/" + f.sibling, "object": map[string]string{"sha": "new-commit"}}})
		} else {
			fmt.Fprint(w, `[]`)
		}
	case strings.HasSuffix(p, "/git/refs"):
		f.ref = true
		f.refCreates++
		fmt.Fprint(w, `{}`)
	case strings.Contains(p, "/git/refs/heads/"):
		if !strings.HasSuffix(p, "/heads/rig-released/"+f.sibling) {
			f.t.Errorf("truncated ref update: %s", q.URL.String())
		}
		f.refUpdates++
		fmt.Fprint(w, `{}`)
	case p == "/repos/acme/app/pulls" && q.Method == "POST":
		f.opens++
		if f.denied {
			w.WriteHeader(403)
			return
		}
		f.pr = true
		f.readBody(q)
		fmt.Fprint(w, `{"number":7}`)
	case p == "/repos/acme/app/pulls/7" && q.Method == "PATCH":
		f.updates++
		f.readBody(q)
		fmt.Fprint(w, `{}`)
	default:
		f.t.Errorf("unexpected API %s %s", q.Method, p)
		w.WriteHeader(404)
	}
}
func (f *releasedPublishFixture) readBody(q *http.Request) {
	var p struct{ Title, Body string }
	if err := json.NewDecoder(q.Body).Decode(&p); err != nil {
		f.t.Error(err)
	}
	f.body = p.Body
	f.title = p.Title
}
func (f *releasedPublishFixture) run(publish bool) (string, error) {
	f.t.Helper()
	c := newStackReleasedCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	args := []string{"app", f.sibling, "--seed", "acme/home@main", "--package", f.pkg, "--via-package", f.via, "--feed", f.feed, "--warning", "ACME0001"}
	if publish && f.omitPair {
		args = args[2:]
	}
	if publish {
		args = append(args, "--publish", f.report)
	} else {
		args = append(args, "--report", f.report)
	}
	c.SetArgs(args)
	err := c.Execute()
	return out.String(), err
}
func (f *releasedPublishFixture) build() {
	f.t.Helper()
	if _, err := f.run(false); err != nil {
		f.t.Fatal(err)
	}
}

func TestStackReleasedPublishExactAndRerun(t *testing.T) {
	f := newReleasedPublishFixture(t)
	f.build()
	if f.writes != 0 {
		t.Fatal("report wrote upstream")
	}
	before, err := os.ReadFile(filepath.Join(f.app, "Code.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "#if STACK_ACME_LIB") {
		t.Fatal("report modified tree")
	}
	for i := 0; i < 2; i++ {
		if i == 1 {
			f.body += "stale text"
		}
		if _, err := f.run(true); err != nil {
			t.Fatal(err)
		}
	}
	if f.opens != 1 || f.updates != 1 || f.refCreates != 1 || f.refUpdates != 0 || f.commits != 1 {
		t.Fatalf("not idempotent: %+v", f)
	}
	if len(f.tree) != 2 || f.tree["Code.cs"] != "Api.New();\n" {
		t.Fatalf("unexpected exact edits: %v", f.tree)
	}
	want := `<Project><PropertyGroup><PackageId>Acme.App</PackageId><Version>1.0.0</Version></PropertyGroup><ItemGroup><PackageReference Include="Acme.Lib" Version="2.0.0" /></ItemGroup></Project>`
	if f.tree["App.csproj"] != want {
		t.Fatalf("project edit %q", f.tree["App.csproj"])
	}
	for _, text := range []string{"compound condition", "Code.cs:1: removed #if STACK_ACME_LIB", "StackOnlyWarning ACME0001", "Complex.cs", "direct reference"} {
		if !strings.Contains(f.body, stackCanaryText(text)) {
			t.Fatalf("missing %s in %s", text, f.body)
		}
	}
	if !strings.Contains(f.title, "Acme.Lib 2.0.0") || !strings.HasSuffix(f.body, "A human must review this PR and run package-mode checks before merging.\n") {
		t.Fatal(f.title, f.body)
	}
}
func TestStackReleasedPublishNotYet(t *testing.T) {
	f := newReleasedPublishFixture(t)
	f.status = "behind"
	f.build()
	data, err := os.ReadFile(f.report)
	if err != nil {
		t.Fatal(err)
	}
	var r stackReleasedReport
	if err = json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "not yet" || len(r.Edits) != 0 {
		t.Fatalf("report: %+v", r)
	}
	if _, err = f.run(true); err != nil {
		t.Fatal(err)
	}
	if f.writes != 0 {
		t.Fatal("not-yet published")
	}
}
func TestStackReleasedPublishReverifies(t *testing.T) {
	f := newReleasedPublishFixture(t)
	f.build()
	f.status = "behind"
	f.comparisons = 0
	if _, err := f.run(true); err == nil {
		t.Fatal("publisher accepted release that no longer proves ancestry")
	}
	if f.comparisons == 0 || f.writes != 0 {
		t.Fatalf("re-verification missing: compares %d writes %d", f.comparisons, f.writes)
	}
}
func TestStackReleasedPublishTampered(t *testing.T) {
	for _, field := range []string{"edit", "version", "warning", "member", "path"} {
		t.Run(field, func(t *testing.T) {
			f := newReleasedPublishFixture(t)
			f.build()
			data, err := os.ReadFile(f.report)
			if err != nil {
				t.Fatal(err)
			}
			var r stackReleasedReport
			if err = json.Unmarshal(data, &r); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "edit":
				r.Edits[0].Content = "tampered"
			case "path":
				r.Edits[0].Path = "../outside"
			case "version":
				r.Version = "9.0.0"
			case "warning":
				r.Options.Warnings = []string{"OTHER"}
			case "member":
				r.Member = "lib"
			}
			data, err = json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(f.report, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = f.run(true); err == nil || f.writes != 0 {
				t.Fatalf("tamper accepted: %v, writes %d", err, f.writes)
			}
		})
	}
}
func TestStackReleasedPublishAlreadyMigrated(t *testing.T) {
	f := newReleasedPublishFixture(t)
	f.build()
	path := filepath.Join(f.app, "App.csproj")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, bytes.Replace(data, []byte(`Version="1.0.0" />`), []byte(`Version="2.0.0" />`), 1), 0600); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, f.app, "add", ".")
	mustGitStack(t, f.app, "commit", "-qm", "hand migration")
	mustGitStack(t, f.app, "push", f.appBare, "main")
	out, err := f.run(true)
	if err != nil || !strings.Contains(out, "already has") || f.writes != 0 {
		t.Fatalf("%s %v writes %d", out, err, f.writes)
	}
}
func TestStackReleasedPublishPermissionDenied(t *testing.T) {
	f := newReleasedPublishFixture(t)
	f.build()
	f.denied = true
	out, err := f.run(true)
	if err != nil || !strings.Contains(out, "Allow GitHub Actions to create and approve pull requests") || !f.ref || f.pr {
		t.Fatalf("%s %v", out, err)
	}
	f.denied = false
	if _, err = f.run(true); err != nil {
		t.Fatal(err)
	}
	if !f.pr || f.refCreates != 1 || f.refUpdates != 0 || f.commits != 1 {
		t.Fatalf("branch not recovered: %+v", f)
	}
}

func TestStackReleasedPublishWithoutPositionals(t *testing.T) {
	f := newReleasedPublishFixture(t)
	f.build()
	f.omitPair = true
	if _, err := f.run(true); err != nil {
		t.Fatal(err)
	}
	if f.opens != 1 {
		t.Fatal("missing PR")
	}
}

func TestStackReleasedPublishStaleDefault(t *testing.T) {
	f := newReleasedPublishFixture(t)
	f.build()
	if err := os.WriteFile(filepath.Join(f.app, "Unrelated.cs"), []byte("class New {}"), 0600); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, f.app, "add", ".")
	mustGitStack(t, f.app, "commit", "-qm", "default moved")
	mustGitStack(t, f.app, "push", f.appBare, "main")
	if _, err := f.run(true); err == nil || f.writes != 0 {
		t.Fatalf("stale default accepted: %v", err)
	}
}

func TestStackReleasedPublishTransitive(t *testing.T) {
	f := newReleasedPublishFixture(t)
	f.sibling = "core"
	f.pkg = "Acme.Core"
	f.via = "Acme.Lib"
	root := filepath.Dir(f.app)
	home := filepath.Join(root, "home")
	lib := filepath.Join(root, "lib")
	core := filepath.Join(root, "core")
	if err := os.MkdirAll(core, 0700); err != nil {
		t.Fatal(err)
	}
	mustGitStack(t, core, "init", "-q", "-b", "main")
	mustGitStack(t, core, "config", "user.name", "test")
	mustGitStack(t, core, "config", "user.email", "test@localhost")
	files := map[string]string{
		filepath.Join(core, "Core.csproj"): `<Project><PropertyGroup><PackageId>Acme.Core</PackageId><Version>1.0.0</Version></PropertyGroup></Project>`,
		filepath.Join(lib, "Lib.csproj"):   `<Project><PropertyGroup><PackageId>Acme.Lib</PackageId><Version>1.0.0</Version></PropertyGroup><ItemGroup><PackageReference Include="Acme.Core" Version="1.0.0" /></ItemGroup></Project>`,
		filepath.Join(f.app, "Code.cs"):    "#if STACK_ACME_CORE\nApi.New();\n#else\nApi.Old();\n#endif\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(home, "rig.stack.jsonc")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest stackManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Repos["core"] = &stackRepo{Upstream: "github.com/acme/core", Fork: "github.com/you/core"}
	manifest.LastSync["core"] = strings.Repeat("a", 40)
	manifest.Proposals["core"] = manifest.Proposals["lib"]
	delete(manifest.Proposals, "lib")
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{home, lib, f.app, core} {
		mustGitStack(t, dir, "add", ".")
		mustGitStack(t, dir, "commit", "-qm", "core dependency")
		bare := filepath.Join(root, filepath.Base(dir)+".git")
		if dir == core {
			mustGitStack(t, dir, "clone", "--bare", dir, bare)
		} else {
			mustGitStack(t, dir, "push", bare, "main")
		}
	}
	t.Setenv("GIT_CONFIG_COUNT", "4")
	t.Setenv("GIT_CONFIG_KEY_3", "url."+filepath.ToSlash(filepath.Join(root, "core.git"))+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_3", "https://github.com/acme/core.git")
	f.build()
	if _, err = f.run(true); err != nil {
		t.Fatal(err)
	}
	if f.opens != 1 || f.tree["Code.cs"] != "Api.New();\n" || !strings.Contains(f.body, stackCanaryText("Acme.Lib 2.0.0 requires Acme.Core >= 2.0.0")) {
		t.Fatalf("transitive PR: %+v", f)
	}
	// An altered delivery nuspec must be independently rechecked too.
	f.deliveryMinimum = "1.0.0"
	f.writes = 0
	if _, err = f.run(true); err == nil || f.writes != 0 {
		t.Fatalf("unverified delivery accepted: %v", err)
	}
}

func TestStackReleasedPublishFailures(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		status             int
		permission         bool
	}{
		{"read500", "GET", "/pulls", 500, false},
		{"read403", "GET", "/git/matching-refs/heads/rig-released/lib", 403, false},
		{"read404", "GET", "/pulls", 404, false},
		{"tree403", "POST", "/git/trees", 403, true},
		{"commit403", "POST", "/git/commits", 403, true},
		{"ref403", "POST", "/git/refs", 403, true},
		{"pr404", "POST", "/pulls", 404, true},
		{"write500", "POST", "/git/trees", 500, false},
		{"write422", "POST", "/git/trees", 422, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReleasedPublishFixture(t)
			f.build()
			f.failMethod, f.failPath, f.failStatus = tc.method, "/repos/acme/app"+tc.path, tc.status
			out, err := f.run(true)
			if tc.permission {
				if err != nil || !strings.Contains(out, "Allow GitHub Actions to create and approve pull requests") || !strings.Contains(out, fmt.Sprintf("HTTP %d", tc.status)) {
					t.Fatalf("missing permission summary: %s %v", out, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", tc.status)) || strings.Contains(out, "Allow GitHub Actions") {
				t.Fatalf("failure hidden or misclassified: %s %v", out, err)
			}
			if tc.method == "GET" && f.writes != 0 {
				t.Fatalf("read failure made %d writes", f.writes)
			}
		})
	}
}

func TestStackReleasedPublishWrongBase(t *testing.T) {
	f := newReleasedPublishFixture(t)
	f.build()
	f.pr = true
	f.ref = true
	f.prBase = "release"
	out, err := f.run(true)
	if err == nil || !strings.Contains(err.Error(), `targets "release" instead of "main"`) || f.writes != 0 {
		t.Fatalf("wrong-base PR modified: %s %v writes %d", out, err, f.writes)
	}
}

func TestStackReleasedPublishChangedHead(t *testing.T) {
	for _, change := range []string{"tree", "parent"} {
		t.Run(change, func(t *testing.T) {
			f := newReleasedPublishFixture(t)
			f.build()
			if _, err := f.run(true); err != nil {
				t.Fatal(err)
			}
			if change == "tree" {
				f.headTree = "old-tree"
			} else {
				f.headParent = "old-base"
			}
			if _, err := f.run(true); err != nil {
				t.Fatal(err)
			}
			if f.opens != 1 || f.refCreates != 1 || f.refUpdates != 1 || f.commits != 2 {
				t.Fatalf("changed head not reconciled: %+v", f)
			}
			if _, err := f.run(true); err != nil {
				t.Fatal(err)
			}
			if f.refUpdates != 1 || f.commits != 2 || f.updates != 0 {
				t.Fatalf("unchanged third run churned: %+v", f)
			}
		})
	}
}

func TestStackReleasedPublishEscapedBranch(t *testing.T) {
	f := newReleasedPublishFixture(t)
	f.build()
	data, err := os.ReadFile(f.report)
	if err != nil {
		t.Fatal(err)
	}
	var r stackReleasedReport
	if err = json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	f.sibling = "lib#suffix"
	r.Sibling = f.sibling
	if err = stackValidPrefix(f.sibling); err != nil {
		t.Fatal("fixture key must be allowed:", err)
	}
	api := stackDepsClient()
	for i := 0; i < 2; i++ {
		if err = stackReleasedPublish(context.Background(), api, "acme/app", "main", r.Bases["app"], r); err != nil {
			t.Fatal(err)
		}
		// Exercise PATCH as well as the matching-refs read and head query.
		f.headTree = "old-tree"
	}
	if f.opens != 1 || f.refCreates != 1 || f.refUpdates != 1 {
		t.Fatalf("escaped branch not reused: %+v", f)
	}
}

func TestStackReleasedReportStreams(t *testing.T) {
	for _, preview := range []bool{false, true} {
		t.Run(fmt.Sprint(preview), func(t *testing.T) {
			f := newReleasedPublishFixture(t)
			old := dryRun
			dryRun = preview
			t.Cleanup(func() { dryRun = old })
			var stdout, stderr bytes.Buffer
			c := newStackReleasedCmd()
			c.SetOut(&stdout)
			c.SetErr(&stderr)
			c.SetArgs([]string{"app", "lib", "--seed", "acme/home@main", "--package", "Acme.Lib", "--feed", f.feed, "--report", f.report})
			if err := c.Execute(); err != nil {
				t.Fatal(err)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "release report") && !strings.Contains(stderr.String(), "Release report") {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			_, err := os.Stat(f.report)
			if preview && !os.IsNotExist(err) || !preview && err != nil {
				t.Fatalf("preview=%v report stat: %v", preview, err)
			}
		})
	}
}

func TestStackReleasedReportOverwriteMode(t *testing.T) {
	f := newReleasedPublishFixture(t)
	if err := os.WriteFile(f.report, bytes.Repeat([]byte("old report"), 1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.report, 0644); err != nil {
		t.Fatal(err)
	}
	f.build()
	data, err := os.ReadFile(f.report)
	if err != nil {
		t.Fatal(err)
	}
	var report stackReleasedReport
	if err = json.Unmarshal(data, &report); err != nil || report.Schema != 1 {
		t.Fatalf("report was not replaced: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(f.report)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0600 {
			t.Fatalf("overwritten report permissions = %04o, want 0600", got)
		}
	}
}

func TestStackReleasedReportRenameFailurePreservesContents(t *testing.T) {
	f := newReleasedPublishFixture(t)
	before := []byte(`{"Schema":1,"Status":"released"}`)
	if err := os.WriteFile(f.report, before, 0644); err != nil {
		t.Fatal(err)
	}
	old := stackReleasedReportRename
	called := false
	stackReleasedReportRename = func(from, to string) error {
		called = true
		if filepath.Dir(from) != filepath.Dir(f.report) || to != f.report {
			t.Errorf("unexpected report replacement: %q -> %q", from, to)
		}
		data, err := os.ReadFile(from)
		if err != nil || !json.Valid(data) {
			t.Errorf("report not prepared before rename: %q %v", data, err)
		}
		return os.ErrPermission
	}
	t.Cleanup(func() { stackReleasedReportRename = old })
	if _, err := f.run(false); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("rename failure not returned: %v", err)
	}
	if !called {
		t.Fatal("replacement was not attempted")
	}
	after, err := os.ReadFile(f.report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("rename failure changed existing report: %q", after)
	}
	if paths, err := filepath.Glob(filepath.Join(filepath.Dir(f.report), ".rig-released-report-*")); err != nil || len(paths) != 0 {
		t.Fatalf("failed report left temporary files: %v %v", paths, err)
	}
}

func TestStackReleasedReportReplacementIsolatesExistingReaders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix rename descriptor isolation; Windows replacement can be denied while the file is open")
	}
	f := newReleasedPublishFixture(t)
	before := []byte(`{"Schema":1,"Status":"old report"}`)
	if err := os.WriteFile(f.report, before, 0644); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(f.report)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	f.build()
	oldContents, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldContents, before) {
		t.Fatalf("existing reader observed replacement contents: %q", oldContents)
	}
	newContents, err := os.ReadFile(f.report)
	if err != nil {
		t.Fatal(err)
	}
	var report stackReleasedReport
	if err = json.Unmarshal(newContents, &report); err != nil || report.Status != "released" || len(report.Edits) == 0 {
		t.Fatalf("replacement report missing: %s %v", newContents, err)
	}
}
