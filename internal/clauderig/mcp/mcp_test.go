package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/settings"
)

// dirs makes an isolated home + repo root for a test.
func dirs(t *testing.T) (home, repo string) {
	t.Helper()
	home = t.TempDir()
	repo = t.TempDir()
	return home, repo
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return m
}

func TestAddGetRemove_UserScope(t *testing.T) {
	home, repo := dirs(t)
	srv := Server{Command: "npx", Args: []string{"-y", "foo"}, Env: map[string]string{"K": "v"}}
	if err := Add(settings.User, home, repo, "foo", srv); err != nil {
		t.Fatal(err)
	}

	// landed in ~/.claude.json under mcpServers
	doc := readJSON(t, filepath.Join(home, ".claude.json"))
	if _, ok := doc["mcpServers"].(map[string]any)["foo"]; !ok {
		t.Fatalf("foo not under mcpServers: %v", doc)
	}

	got, ok, err := Get(settings.User, home, repo, "foo")
	if err != nil || !ok {
		t.Fatalf("Get foo: ok=%v err=%v", ok, err)
	}
	if got.Command != "npx" || got.Transport() != TransportStdio || got.Env["K"] != "v" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	removed, err := Remove(settings.User, home, repo, "foo")
	if err != nil || !removed {
		t.Fatalf("Remove foo: removed=%v err=%v", removed, err)
	}
	if _, ok, _ := Get(settings.User, home, repo, "foo"); ok {
		t.Fatal("foo still present after remove")
	}
}

func TestAdd_ProjectAndLocalScopeLocations(t *testing.T) {
	home, repo := dirs(t)
	if err := Add(settings.Project, home, repo, "p", Server{Command: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := Add(settings.Local, home, repo, "l", Server{Type: TransportHTTP, URL: "https://x"}); err != nil {
		t.Fatal(err)
	}

	// project → <repo>/.mcp.json
	if _, err := os.Stat(filepath.Join(repo, ".mcp.json")); err != nil {
		t.Fatalf(".mcp.json missing: %v", err)
	}
	// local → ~/.claude.json projects[repo].mcpServers
	doc := readJSON(t, filepath.Join(home, ".claude.json"))
	proj, ok := doc["projects"].(map[string]any)[repo].(map[string]any)
	if !ok {
		t.Fatalf("projects[%s] missing: %v", repo, doc)
	}
	if _, ok := proj["mcpServers"].(map[string]any)["l"]; !ok {
		t.Fatalf("local server not nested under projects[repo].mcpServers: %v", proj)
	}

	got, ok, err := Get(settings.Local, home, repo, "l")
	if err != nil || !ok {
		t.Fatalf("Get local l: ok=%v err=%v", ok, err)
	}
	if got.Transport() != TransportHTTP || got.URL != "https://x" {
		t.Fatalf("local server mismatch: %+v", got)
	}
}

func TestAdd_PreservesOtherServersAndKeys(t *testing.T) {
	home, repo := dirs(t)
	// Pre-seed ~/.claude.json with an unrelated top-level key and a server that
	// carries a field clauderig's struct doesn't model.
	seed := map[string]any{
		"numStartups": float64(7),
		"mcpServers": map[string]any{
			"keep": map[string]any{"command": "keep", "futureField": "x"},
		},
	}
	b, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Add(settings.User, home, repo, "added", Server{Command: "added"}); err != nil {
		t.Fatal(err)
	}

	doc := readJSON(t, filepath.Join(home, ".claude.json"))
	if doc["numStartups"].(float64) != 7 {
		t.Errorf("top-level key clobbered: %v", doc["numStartups"])
	}
	keep := doc["mcpServers"].(map[string]any)["keep"].(map[string]any)
	if keep["futureField"] != "x" {
		t.Errorf("unknown field on other server lost: %v", keep)
	}
	if _, ok := doc["mcpServers"].(map[string]any)["added"]; !ok {
		t.Error("added server missing")
	}
}

func TestList_AcrossScopesWithState(t *testing.T) {
	home, repo := dirs(t)
	mustAdd(t, settings.User, home, repo, "u", Server{Command: "u"})
	mustAdd(t, settings.Project, home, repo, "papproved", Server{Command: "p1"})
	mustAdd(t, settings.Project, home, repo, "ppending", Server{Command: "p2"})
	mustAdd(t, settings.Project, home, repo, "pblocked", Server{Command: "p3"})

	if err := SetEnabled(home, repo, "papproved", true); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(home, repo, "pblocked", false); err != nil {
		t.Fatal(err)
	}

	entries, err := List(home, repo)
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]State{}
	scope := map[string]settings.Scope{}
	for _, e := range entries {
		state[e.Name] = e.State
		scope[e.Name] = e.Scope
	}
	if scope["u"] != settings.User || state["u"] != StateNA {
		t.Errorf("user server state/scope wrong: %v/%v", scope["u"], state["u"])
	}
	if state["papproved"] != StateEnabled {
		t.Errorf("papproved = %q, want enabled", state["papproved"])
	}
	if state["ppending"] != StatePending {
		t.Errorf("ppending = %q, want pending", state["ppending"])
	}
	if state["pblocked"] != StateDisabled {
		t.Errorf("pblocked = %q, want disabled", state["pblocked"])
	}
}

func TestSetEnabled_TogglesExclusively(t *testing.T) {
	home, repo := dirs(t)
	if err := SetEnabled(home, repo, "x", true); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(home, repo, "x", false); err != nil {
		t.Fatal(err)
	}
	local, _ := settings.Local.Path(home, repo)
	m := readJSON(t, local)
	if dl := stringList(m["disabledMcpjsonServers"]); len(dl) != 1 || dl[0] != "x" {
		t.Errorf("disabled = %v, want [x]", dl)
	}
	// flipping to disabled should have cleared the enabled list entirely
	if _, ok := m["enabledMcpjsonServers"]; ok {
		if el := stringList(m["enabledMcpjsonServers"]); len(el) != 0 {
			t.Errorf("enabled = %v, want empty", el)
		}
	}
}

func TestList_NoRepoSkipsProjectAndLocal(t *testing.T) {
	home, repo := dirs(t)
	mustAdd(t, settings.User, home, repo, "u", Server{Command: "u"})
	mustAdd(t, settings.Project, home, repo, "p", Server{Command: "p"})

	entries, err := List(home, "") // no repo
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "u" {
		t.Fatalf("want only user server, got %v", entries)
	}
}

func mustAdd(t *testing.T, scope settings.Scope, home, repo, name string, srv Server) {
	t.Helper()
	if err := Add(scope, home, repo, name, srv); err != nil {
		t.Fatalf("add %s/%s: %v", scope, name, err)
	}
}
