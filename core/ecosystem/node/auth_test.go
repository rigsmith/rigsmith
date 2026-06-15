package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
