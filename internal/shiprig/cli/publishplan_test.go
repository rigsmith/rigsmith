package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
	"github.com/rigsmith/rigsmith/internal/changerig/commands"
)

// fakeRegistry is the built-in node adapter with its registry replaced: the
// published names answer true, a name in fail errors, the rest answer false.
type fakeRegistry struct {
	plugin.Ecosystem
	published map[string]bool
	fail      map[string]bool
}

func (f fakeRegistry) Published(_ context.Context, req plugin.PublishedRequest) (plugin.PublishedResponse, error) {
	if f.fail[req.Package.Name] {
		return plugin.PublishedResponse{}, errors.New("registry unreachable")
	}
	return plugin.PublishedResponse{Published: f.published[req.Package.Name]}, nil
}

// planRepo is an npm workspace, committed on main: app depends on lib, done is
// already published, and priv is private. config is .changeset/config.json.
func planRepo(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".changeset/config.json", config)
	write("package.json", `{ "name": "root", "private": true, "workspaces": ["packages/*"] }`)
	write("package-lock.json", "{}")
	write("packages/lib/package.json", `{ "name": "lib", "version": "1.0.0" }`)
	write("packages/app/package.json", `{ "name": "app", "version": "2.0.0", "dependencies": { "lib": "^1.0.0" } }`)
	write("packages/done/package.json", `{ "name": "done", "version": "1.0.0" }`)
	write("packages/priv/package.json", `{ "name": "priv", "version": "1.0.0", "private": true }`)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=x@y.z", "-c", "user.name=x", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Chdir(root)
	return root
}

// planFor builds the plan with the fake registry standing in for npm.
func planFor(t *testing.T, fake fakeRegistry, distTag string) ([][]planRelease, error) {
	t.Helper()
	ws, err := commands.Open()
	if err != nil {
		t.Fatal(err)
	}
	node, ok := ws.EcosystemFor("node")
	if !ok {
		t.Fatal("no node adapter")
	}
	fake.Ecosystem = node
	ws.Registry.Register(fake)
	return buildPublishPlan(context.Background(), ws, distTag)
}

func names(plan [][]planRelease) [][]string {
	var out [][]string
	for _, chunk := range plan {
		var c []string
		for _, r := range chunk {
			c = append(c, r.Kind+":"+r.Name)
		}
		out = append(out, c)
	}
	return out
}

func TestPublishPlanOrdersByDependencyAndSkipsWhatIsPublished(t *testing.T) {
	planRepo(t, `{ "privatePackages": { "version": true, "tag": true } }`)
	plan, err := planFor(t, fakeRegistry{published: map[string]bool{"done": true}}, "")
	if err != nil {
		t.Fatal(err)
	}
	// lib before app, which depends on it; done is already published; priv is
	// private with privatePackages.tag, and untagged.
	want := [][]string{{"publish:lib", "tag-only:priv"}, {"publish:app"}}
	if got := names(plan); !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
	lib := plan[0][0]
	if lib.Version != "1.0.0" || lib.Tag != "latest" || lib.Ecosystem != "node" {
		t.Errorf("lib = %+v", lib)
	}
}

func TestPublishPlanLeavesOutTaggedAndUntaggablePrivatePackages(t *testing.T) {
	root := planRepo(t, `{ "privatePackages": { "version": true, "tag": true } }`)
	if out, err := exec.Command("git", "-C", root, "tag", "priv@1.0.0").CombinedOutput(); err != nil {
		t.Fatalf("git tag: %v\n%s", err, out)
	}
	plan, err := planFor(t, fakeRegistry{published: map[string]bool{"done": true, "lib": true, "app": true}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 0 {
		t.Fatalf("plan = %v, want empty: priv's tag exists", names(plan))
	}

	// Without privatePackages.tag, a private package isn't tagged at all.
	planRepo(t, `{}`)
	plan, err = planFor(t, fakeRegistry{published: map[string]bool{"done": true, "lib": true, "app": true}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 0 {
		t.Fatalf("plan = %v, want empty: priv is never tagged", names(plan))
	}
}

// A registry that can't be asked fails the plan, naming the package.
func TestPublishPlanFailsWhenARegistryCantBeAsked(t *testing.T) {
	planRepo(t, `{}`)
	_, err := planFor(t, fakeRegistry{fail: map[string]bool{"lib": true}}, "")
	if err == nil || !strings.Contains(err.Error(), "lib") {
		t.Fatalf("err = %v, want a failure naming lib", err)
	}
}

func TestPublishPlanDistTag(t *testing.T) {
	root := planRepo(t, `{}`)
	fake := fakeRegistry{published: map[string]bool{"done": true, "app": true}}

	plan, err := planFor(t, fake, "canary")
	if err != nil {
		t.Fatal(err)
	}
	if plan[0][0].Tag != "canary" {
		t.Errorf("--tag canary: tag = %q", plan[0][0].Tag)
	}

	// In pre mode the prerelease tag is the default.
	if err := os.WriteFile(filepath.Join(root, ".changeset", "pre.json"),
		[]byte(`{ "mode": "pre", "tag": "next", "initialVersions": {}, "changesets": [] }`), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err = planFor(t, fake, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan[0][0].Tag != "next" {
		t.Errorf("pre mode: tag = %q, want next", plan[0][0].Tag)
	}
}

func TestPublishPlanFileIsCanonsFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out", "publish-plan.json")
	if err := writePublishPlan(path, nil); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	// An empty plan is an empty list, as canon writes it, not null.
	if got["version"] != float64(1) || !reflect.DeepEqual(got["plan"], []any{}) {
		t.Errorf("plan file = %s", data)
	}
}

func TestChunkByDependenciesPutsACycleLast(t *testing.T) {
	dep := func(n string) []plugin.Dependency { return []plugin.Dependency{{Name: n, Kind: plugin.DepNormal}} }
	pkgs := map[string]plugin.Package{
		"base": {Name: "base"},
		"x":    {Name: "x", Dependencies: dep("y")},
		"y":    {Name: "y", Dependencies: dep("x")},
		"devs": {Name: "devs", Dependencies: []plugin.Dependency{{Name: "x", Kind: plugin.DepDev}}},
	}
	var releases []planRelease
	for _, n := range []string{"x", "y", "base", "devs"} {
		releases = append(releases, planRelease{Kind: "publish", Name: n})
	}
	got := names(chunkByDependencies(releases, pkgs))
	// A dev dependency doesn't order anything; the x/y cycle goes out last.
	want := [][]string{{"publish:base", "publish:devs"}, {"publish:x", "publish:y"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chunks = %v, want %v", got, want)
	}
}
