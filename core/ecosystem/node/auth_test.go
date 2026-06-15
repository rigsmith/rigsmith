package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestReleaseInit_OIDCMakesTokenOptional(t *testing.T) {
	a := &Adapter{}

	withOIDC, err := a.ReleaseInit(context.Background(), plugin.ReleaseInitRequest{OIDC: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(withOIDC.Tokens) != 0 {
		t.Errorf("OIDC on: want no required tokens, got %+v", withOIDC.Tokens)
	}
	if !joinedHas(withOIDC.Notes, "Trusted Publisher") {
		t.Errorf("OIDC on: missing trusted-publisher setup note: %v", withOIDC.Notes)
	}

	off, err := a.ReleaseInit(context.Background(), plugin.ReleaseInitRequest{OIDC: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Tokens) != 1 || off.Tokens[0].EnvVar != "NPM_TOKEN" {
		t.Errorf("OIDC off: want NPM_TOKEN required, got %+v", off.Tokens)
	}
}

func joinedHas(notes []string, substr string) bool {
	return strings.Contains(strings.Join(notes, "\n"), substr)
}

func TestNpmrcAuthKey(t *testing.T) {
	cases := []struct {
		name          string
		packageSource string
		want          string
	}{
		{"default registry", "", "//registry.npmjs.org/:_authToken="},
		{"feed name", "npm", "//registry.npmjs.org/:_authToken="},
		{"url no path", "https://npm.pkg.github.com", "//npm.pkg.github.com/:_authToken="},
		{"url with path", "https://my.registry/api/npm/", "//my.registry/api/npm/:_authToken="},
		{"url path no slash", "https://my.registry/api/npm", "//my.registry/api/npm/:_authToken="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := npmrcAuthKey(tc.packageSource); got != tc.want {
				t.Errorf("npmrcAuthKey(%q) = %q, want %q", tc.packageSource, got, tc.want)
			}
		})
	}
}

func TestNpmAuthConfig_SeedsAndAppends(t *testing.T) {
	// Point npm's user config at a seed file with a scope setting; the temp
	// npmrc must preserve it and append our auth line.
	home := t.TempDir()
	seed := filepath.Join(home, ".npmrc")
	if err := os.WriteFile(seed, []byte("@acme:registry=https://registry.npmjs.org/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NPM_CONFIG_USERCONFIG", seed)

	env, cleanup, err := npmAuthConfig("tok-123", "")
	if err != nil {
		t.Fatalf("npmAuthConfig: %v", err)
	}
	defer cleanup()

	var npmrcPath string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "NPM_CONFIG_USERCONFIG="); ok {
			npmrcPath = v
		}
	}
	if npmrcPath == "" || npmrcPath == seed {
		t.Fatalf("expected a fresh temp npmrc, got %q", npmrcPath)
	}
	data, err := os.ReadFile(npmrcPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "@acme:registry=") {
		t.Errorf("seed config not preserved:\n%s", content)
	}
	if !strings.Contains(content, "//registry.npmjs.org/:_authToken=tok-123") {
		t.Errorf("auth line not appended:\n%s", content)
	}

	// cleanup removes the temp file.
	cleanup()
	if _, err := os.Stat(npmrcPath); !os.IsNotExist(err) {
		t.Errorf("temp npmrc not cleaned up: %v", err)
	}
}
