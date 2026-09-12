package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/mcp"
)

func TestBuildServer_Stdio(t *testing.T) {
	srv, err := buildServer(mcp.TransportStdio, []string{"npx", "-y", "pkg"}, []string{"K=v"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if srv.Transport() != mcp.TransportStdio || srv.Command != "npx" {
		t.Fatalf("transport/command wrong: %+v", srv)
	}
	if strings.Join(srv.Args, " ") != "-y pkg" {
		t.Errorf("args = %v, want [-y pkg]", srv.Args)
	}
	if srv.Env["K"] != "v" {
		t.Errorf("env = %v", srv.Env)
	}
}

func TestBuildServer_HTTPWithHeader(t *testing.T) {
	srv, err := buildServer(mcp.TransportHTTP, []string{"https://x/mcp"}, nil, []string{"Authorization=Bearer z"})
	if err != nil {
		t.Fatal(err)
	}
	if srv.Transport() != mcp.TransportHTTP || srv.URL != "https://x/mcp" {
		t.Fatalf("transport/url wrong: %+v", srv)
	}
	if srv.Headers["Authorization"] != "Bearer z" {
		t.Errorf("headers = %v", srv.Headers)
	}
}

// A header passed as a positional (forgetting -H) must be rejected, not dropped.
func TestBuildServer_HTTPRejectsExtraArgs(t *testing.T) {
	_, err := buildServer(mcp.TransportHTTP, []string{"https://x/mcp", "Authorization=Bearer z"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for extra positional after URL")
	}
	if !strings.Contains(err.Error(), "single URL") {
		t.Errorf("error = %q, want it to mention the single-URL rule", err)
	}
}

func TestBuildServer_MissingTarget(t *testing.T) {
	if _, err := buildServer(mcp.TransportStdio, nil, nil, nil); err == nil {
		t.Error("stdio with no command should error")
	}
	if _, err := buildServer(mcp.TransportSSE, nil, nil, nil); err == nil {
		t.Error("sse with no url should error")
	}
}

func TestBuildServer_UnknownTransport(t *testing.T) {
	if _, err := buildServer("bogus", []string{"x"}, nil, nil); err == nil {
		t.Error("unknown transport should error")
	}
}

func TestParseKV(t *testing.T) {
	m, err := parseKV([]string{"A=1", "B=x=y"})
	if err != nil {
		t.Fatal(err)
	}
	if m["A"] != "1" || m["B"] != "x=y" {
		t.Errorf("parseKV = %v", m)
	}
	if _, err := parseKV([]string{"noequals"}); err == nil {
		t.Error("missing = should error")
	}
	if _, err := parseKV([]string{"=v"}); err == nil {
		t.Error("empty key should error")
	}
}

// The Judge tests are synthetic: they hand Judge a Carriage and check what it
// says. Nothing exercised the part that decides the Carriage — a real git
// repository, a real .mcp.json, and the boundary between them — so the whole
// verdict could regress with every portability test green.
func TestProjectFileCarriage_AgainstARealRepository(t *testing.T) {
	git := func(t *testing.T, dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(t *testing.T, dir, name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const tidy = `{"mcpServers":{"tidy":{"command":"npx","args":["-y","@acme/tidy-mcp"]}}}`

	for _, tc := range []struct {
		name     string
		setup    func(t *testing.T, dir string)
		want     mcp.Carriage
		wantDefs bool
	}{
		{"committed", func(t *testing.T, dir string) {
			write(t, dir, ".mcp.json", tidy)
			git(t, dir, "add", ".mcp.json")
			git(t, dir, "commit", "-qm", "add")
		}, mcp.CarriageTracked, true},
		{"staged only", func(t *testing.T, dir string) {
			write(t, dir, "seed", "x")
			git(t, dir, "add", "seed")
			git(t, dir, "commit", "-qm", "seed")
			write(t, dir, ".mcp.json", tidy)
			git(t, dir, "add", ".mcp.json") // in the index, never committed
		}, mcp.CarriageUntracked, false},
		{"ignored", func(t *testing.T, dir string) {
			write(t, dir, ".gitignore", ".mcp.json\n")
			git(t, dir, "add", ".gitignore")
			git(t, dir, "commit", "-qm", "ignore")
			write(t, dir, ".mcp.json", tidy)
		}, mcp.CarriageIgnored, false},
		{"never added", func(t *testing.T, dir string) {
			write(t, dir, "seed", "x")
			git(t, dir, "add", "seed")
			git(t, dir, "commit", "-qm", "seed")
			write(t, dir, ".mcp.json", tidy)
		}, mcp.CarriageUntracked, false},
		{"committed but malformed", func(t *testing.T, dir string) {
			write(t, dir, ".mcp.json", `{"mcpServers":[]}`)
			git(t, dir, "add", ".mcp.json")
			git(t, dir, "commit", "-qm", "bad")
			write(t, dir, ".mcp.json", tidy) // working copy is fine
		}, mcp.CarriageUnknown, false},
		{"not a repository", func(t *testing.T, dir string) {
			write(t, dir, ".mcp.json", tidy)
		}, mcp.CarriageUnknown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.name != "not a repository" {
				git(t, dir, "init", "-q", "-b", "main")
			}
			tc.setup(t, dir)
			got, defs := projectFileCarriage(t.Context(), dir)
			if got != tc.want {
				t.Errorf("carriage = %v, want %v", got, tc.want)
			}
			if (defs != nil) != tc.wantDefs {
				t.Errorf("defs = %v, want present=%v", defs, tc.wantDefs)
			}
			if tc.wantDefs && defs["tidy"] == "" {
				t.Errorf("the committed server was not fingerprinted: %v", defs)
			}
		})
	}
}
