package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

// writeFiles lays out files (path → content) under root.
func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A workspace repo with one package at packages/lib.
func workspace(t *testing.T, files map[string]string) (repo string, pkg plugin.Package) {
	t.Helper()
	repo = filepath.Join(t.TempDir(), "repo")
	all := map[string]string{"packages/lib/package.json": `{"name":"@acme/lib","version":"1.2.0"}`}
	for k, v := range files {
		all[k] = v
	}
	writeFiles(t, repo, all)
	return repo, plugin.Package{Name: "@acme/lib", Version: "1.2.0", Dir: "packages/lib"}
}

func TestNpmRegistry(t *testing.T) {
	const private = "https://npm.acme.example/"
	for _, tc := range []struct {
		name   string
		files  map[string]string
		source string
		want   string
	}{
		// The bug: a pnpm workspace routes its scope in the ROOT .npmrc; npm, run in the package directory, never
		// reads it.
		{"the workspace root .npmrc routes the scope", map[string]string{".npmrc": "@acme:registry=" + private}, "", private},
		{"the package's own .npmrc", map[string]string{"packages/lib/.npmrc": "@acme:registry=" + private}, "", private},
		{"the nearest file wins", map[string]string{
			".npmrc":              "@acme:registry=https://root.example/",
			"packages/lib/.npmrc": "@acme:registry=" + private,
		}, "", private},
		{"a scoped entry beats a nearer plain registry", map[string]string{
			".npmrc":              "@acme:registry=" + private,
			"packages/lib/.npmrc": "registry=https://mirror.example/",
		}, "", private},
		{"another scope's routing is not ours", map[string]string{".npmrc": "@other:registry=https://other.example/"}, "", ""},
		{"a plain registry when the scope has no routing", map[string]string{".npmrc": "registry=" + private}, "", private},
		{"publishConfig.registry beats .npmrc", map[string]string{
			".npmrc":                    "@acme:registry=https://root.example/",
			"packages/lib/package.json": `{"name":"@acme/lib","version":"1.2.0","publishConfig":{"registry":"` + private + `"}}`,
		}, "", private},
		{"a configured source beats everything", map[string]string{".npmrc": "@acme:registry=https://root.example/"}, private, private},
		{"quotes, spaces and comments", map[string]string{".npmrc": "# routing\n; also a comment\n@acme:registry = \"" + private + "\"\n"}, "", private},
		{"nothing configured is npm's default", nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, pkg := workspace(t, tc.files)
			if got := npmRegistry(repo, pkg, tc.source); got != tc.want {
				t.Errorf("npmRegistry = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNpmRegistryExpandsEnvReferences(t *testing.T) {
	t.Setenv("ACME_REGISTRY", "https://npm.acme.example/")
	repo, pkg := workspace(t, map[string]string{".npmrc": "@acme:registry=${ACME_REGISTRY}"})
	if got := npmRegistry(repo, pkg, ""); got != "https://npm.acme.example/" {
		t.Errorf("npmRegistry = %q", got)
	}
}

// The walk stops at the repository root: an .npmrc above it is the user's or a parent project's, which npm reads
// (or not) on its own terms.
func TestNpmRegistryStopsAtTheRepositoryRoot(t *testing.T) {
	repo, pkg := workspace(t, nil)
	writeFiles(t, filepath.Dir(repo), map[string]string{".npmrc": "@acme:registry=https://outside.example/"})
	for _, root := range []string{repo, repo + string(filepath.Separator)} {
		if got := npmRegistry(root, pkg, ""); got != "" {
			t.Errorf("npmRegistry(%q) = %q, want npm's default (\"\")", root, got)
		}
	}
}

// End to end through the adapter: publish-plan's lookup asks the workspace's registry, not npmjs.com.
func TestPublishedAsksTheWorkspaceRegistry(t *testing.T) {
	repo, pkg := workspace(t, map[string]string{".npmrc": "@acme:registry=https://npm.acme.example/"})
	got := stubNpmView(t, "1.2.0", "", nil)
	resp, err := (&Adapter{}).Published(context.Background(), plugin.PublishedRequest{RepoRoot: repo, Package: pkg})
	if err != nil || !resp.Published {
		t.Fatalf("resp = %+v, err = %v, want Published", resp, err)
	}
	if !slices.Equal(*got, []string{"@acme/lib@1.2.0", "version", "--registry", "https://npm.acme.example/"}) {
		t.Errorf("npm view args = %v", *got)
	}
}

// ...and publish sends it there too — checking and publishing against the same registry. Before, the pre-check and
// `npm publish` both ran in the package directory and would have published a private package to npmjs.com.
func TestPublishUsesTheWorkspaceRegistry(t *testing.T) {
	repo, pkg := workspace(t, map[string]string{".npmrc": "@acme:registry=https://npm.acme.example/"})
	view := stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	var got []string
	was := npmPublish
	npmPublish = func(_ context.Context, _ string, _ []string, args ...string) (string, string, error) {
		got = args
		return "", "", nil
	}
	t.Cleanup(func() { npmPublish = was })

	if _, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: pkg, Access: "restricted"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*view, []string{"@acme/lib@1.2.0", "version", "--registry", "https://npm.acme.example/"}) {
		t.Errorf("pre-check npm view args = %v", *view)
	}
	if !slices.Equal(got, []string{"publish", "--access", "restricted", "--registry", "https://npm.acme.example/"}) {
		t.Errorf("npm publish args = %v", got)
	}
}
