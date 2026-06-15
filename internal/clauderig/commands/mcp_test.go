package commands

import (
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
