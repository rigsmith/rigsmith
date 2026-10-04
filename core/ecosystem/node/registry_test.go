package node

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
			got, err := npmRegistry(repo, pkg, tc.source)
			if err != nil || got != tc.want {
				t.Errorf("npmRegistry = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}

func TestNpmRegistryExpandsEnvReferences(t *testing.T) {
	t.Setenv("ACME_REGISTRY", "https://npm.acme.example/")
	repo, pkg := workspace(t, map[string]string{".npmrc": "@acme:registry=${ACME_REGISTRY}"})
	if got, err := npmRegistry(repo, pkg, ""); err != nil || got != "https://npm.acme.example/" {
		t.Errorf("npmRegistry = %q, %v", got, err)
	}
}

// An unset reference in the chosen registry is an error, as in npm — never a silent fall back to the default, which
// would send a private package to npmjs.com.
func TestNpmRegistryRefusesAnUnsetReference(t *testing.T) {
	for name, line := range map[string]string{
		"unset":            "@acme:registry=${ACME_UNSET_REGISTRY}",
		"empty when set":   "@acme:registry=${ACME_EMPTY_REGISTRY}",
		"a plain registry": "registry=${ACME_UNSET_REGISTRY}",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ACME_EMPTY_REGISTRY", "")
			os.Unsetenv("ACME_UNSET_REGISTRY")
			repo, pkg := workspace(t, map[string]string{".npmrc": line})
			got, err := npmRegistry(repo, pkg, "")
			if err == nil {
				t.Fatalf("npmRegistry = %q, want an error", got)
			}
			if !strings.Contains(err.Error(), ".npmrc") {
				t.Errorf("error should name the file: %v", err)
			}
		})
	}
}

// ...but only the value actually used: an unset token reference beside the routing is the usual layout, and fine.
func TestNpmRegistryIgnoresUnsetReferencesItDoesNotUse(t *testing.T) {
	os.Unsetenv("ACME_UNSET_TOKEN")
	repo, pkg := workspace(t, map[string]string{".npmrc": "//npm.acme.example/:_authToken=${ACME_UNSET_TOKEN}\n@acme:registry=https://npm.acme.example/"})
	if got, err := npmRegistry(repo, pkg, ""); err != nil || got != "https://npm.acme.example/" {
		t.Errorf("npmRegistry = %q, %v", got, err)
	}
}

func TestPublishRefusesAnUnresolvedRegistry(t *testing.T) {
	os.Unsetenv("ACME_UNSET_REGISTRY")
	repo, pkg := workspace(t, map[string]string{".npmrc": "@acme:registry=${ACME_UNSET_REGISTRY}"})
	stubNpmView(t, "", "", errors.New("npm must not be asked"))
	was := npmPublish
	npmPublish = func(context.Context, string, []string, ...string) (string, string, error) {
		t.Error("npm publish must not run")
		return "", "", nil
	}
	t.Cleanup(func() { npmPublish = was })
	if _, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: pkg}); err == nil {
		t.Error("Publish succeeded, want an error")
	}
	if _, err := (&Adapter{}).Published(context.Background(), plugin.PublishedRequest{RepoRoot: repo, Package: pkg}); err == nil {
		t.Error("Published succeeded, want an error")
	}
}

// The walk stops at the repository root: an .npmrc above it is the user's or a parent project's, which npm reads
// (or not) on its own terms.
func TestNpmRegistryStopsAtTheRepositoryRoot(t *testing.T) {
	repo, pkg := workspace(t, nil)
	writeFiles(t, filepath.Dir(repo), map[string]string{".npmrc": "@acme:registry=https://outside.example/"})
	for _, root := range []string{repo, repo + string(filepath.Separator)} {
		if got, err := npmRegistry(root, pkg, ""); err != nil || got != "" {
			t.Errorf("npmRegistry(%q) = %q, %v, want npm's default (\"\")", root, got, err)
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
	if !slices.Equal(*got, []string{"@acme/lib@1.2.0", "version", "--registry", "https://npm.acme.example/", "--@acme:registry=https://npm.acme.example/"}) {
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
	if !slices.Equal(*view, []string{"@acme/lib@1.2.0", "version", "--registry", "https://npm.acme.example/", "--@acme:registry=https://npm.acme.example/"}) {
		t.Errorf("pre-check npm view args = %v", *view)
	}
	if !slices.Equal(got, []string{"publish", "--access", "restricted", "--registry", "https://npm.acme.example/", "--@acme:registry=https://npm.acme.example/"}) {
		t.Errorf("npm publish args = %v", got)
	}
}

// The flags must decide where npm actually goes, not just what it is told: npm picks a scoped package's registry from
// `@scope:registry` in any config layer before `registry`, so a user ~/.npmrc routing the scope elsewhere beat a bare
// --registry. Asked of real npm (skipped without it): with the user config routing @acme to a.invalid, npm view must
// contact b.invalid — and with --registry alone it would have contacted a.invalid, which is why the scoped flag is there.
func TestNpmRegistryArgsDecideWhereNpmGoes(t *testing.T) {
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not installed")
	}
	dir := t.TempDir()
	userrc := filepath.Join(dir, "userrc")
	writeFiles(t, dir, map[string]string{"userrc": "@acme:registry=https://a.invalid/\n"})

	contacted := func(args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "npm", append([]string{"view", "@acme/lib", "version", "--fetch-retries=0", "--loglevel=http"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "NPM_CONFIG_USERCONFIG="+userrc)
		out, _ := cmd.CombinedOutput() // the hosts don't exist: npm fails, and says where it tried
		switch {
		case strings.Contains(string(out), "b.invalid"):
			return "b"
		case strings.Contains(string(out), "a.invalid"):
			return "a"
		}
		return "neither: " + string(out)
	}
	if got := contacted(npmRegistryArgs("@acme/lib", "https://b.invalid/")...); got != "b" {
		t.Errorf("with npmRegistryArgs, npm contacted %s, want b", got)
	}
	if got := contacted("--registry", "https://b.invalid/"); got != "a" {
		t.Logf("note: --registry alone reached %s; npm's scoped precedence may have changed", got)
	}
}

// A package directory that climbs out of the repository is refused before anything outside is read — compared by path
// component, so a sibling whose name merely starts with the repository's (/tmp/repository beside /tmp/repo) is outside.
func TestNpmRegistryRefusesAPackageOutsideTheRepository(t *testing.T) {
	repo, _ := workspace(t, nil)
	writeFiles(t, filepath.Dir(repo), map[string]string{
		"repository/lib/package.json": `{"name":"@acme/lib","version":"1.2.0"}`,
		"repository/.npmrc":           "@acme:registry=https://sibling.example/",
	})
	pkg := plugin.Package{Name: "@acme/lib", Version: "1.2.0", Dir: "../repository/lib"}
	got, err := npmRegistry(repo, pkg, "")
	if err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Errorf("npmRegistry = %q, %v, want an outside-the-repository error", got, err)
	}
}

// What can't be read or understood is an error, not an absence: guessing past it could route the package somewhere
// it never named.
func TestNpmRegistrySurfacesUnreadableConfig(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"a malformed package.json":  {"packages/lib/package.json": `{"name":`},
		"a non-string registry":     {"packages/lib/package.json": `{"name":"@acme/lib","publishConfig":{"registry":42}}`},
		"an .npmrc that won't read": {".npmrc/oops": "a directory where the file should be"},
	} {
		t.Run(name, func(t *testing.T) {
			repo, pkg := workspace(t, files)
			if got, err := npmRegistry(repo, pkg, ""); err == nil {
				t.Errorf("npmRegistry = %q, want an error", got)
			}
		})
	}
	// ...while a readable manifest that simply names no registry is fine.
	repo, pkg := workspace(t, map[string]string{"packages/lib/package.json": `{"name":"@acme/lib","publishConfig":{"access":"public"}}`})
	if got, err := npmRegistry(repo, pkg, ""); err != nil || got != "" {
		t.Errorf("npmRegistry = %q, %v, want npm's default", got, err)
	}
}

// A private feed that refuses the anonymous lookup is asked once more with the publish credential, keyed to its own
// host; one that answers is never sent it.
func TestPublishedSendsTheCredentialOnlyWhenAsked(t *testing.T) {
	repo, pkg := workspace(t, map[string]string{".npmrc": "@acme:registry=https://npm.acme.example/"})
	type call struct{ npmrc string }
	stub := func(anonymous error) *[]call {
		var calls []call
		was := npmView
		npmView = func(_ context.Context, _ string, env []string, _ ...string) (string, string, error) {
			c := call{}
			for _, kv := range env {
				if p, ok := strings.CutPrefix(kv, "NPM_CONFIG_USERCONFIG="); ok {
					data, _ := os.ReadFile(p)
					c.npmrc = string(data)
				}
			}
			calls = append(calls, c)
			if env == nil && anonymous != nil {
				return "", "npm error code E401", anonymous
			}
			return "1.2.0", "", nil
		}
		t.Cleanup(func() { npmView = was })
		return &calls
	}
	req := plugin.PublishedRequest{RepoRoot: repo, Package: pkg, Auth: &plugin.AuthCredential{Token: "feed-token"}}

	calls := stub(errors.New("exit status 1"))
	resp, err := (&Adapter{}).Published(context.Background(), req)
	if err != nil || !resp.Published {
		t.Fatalf("resp = %+v, err = %v, want Published", resp, err)
	}
	if len(*calls) != 2 || !strings.Contains((*calls)[1].npmrc, "//npm.acme.example/:_authToken=feed-token") {
		t.Errorf("calls = %+v, want an anonymous lookup then one with the token keyed to npm.acme.example", *calls)
	}

	calls = stub(nil)
	if _, err := (&Adapter{}).Published(context.Background(), req); err != nil || len(*calls) != 1 || (*calls)[0].npmrc != "" {
		t.Errorf("calls = %+v, err = %v, want one anonymous lookup", *calls, err)
	}

	calls = stub(errors.New("exit status 1"))
	req.Auth = nil
	if _, err := (&Adapter{}).Published(context.Background(), req); err == nil || len(*calls) != 1 {
		t.Errorf("calls = %+v, err = %v, want the E401 as an error with no credential to retry with", *calls, err)
	}
}

// OIDC's CI id-token is exchanged where it always was — the configured source, else npmjs.com — and never at a
// registry found in an .npmrc or publishConfig. shiprig turns OIDC on by itself in CI, so a package routed elsewhere
// is not refused: OIDC is skipped and npm publishes with its own auth, no token minted.
func TestPublishKeepsOIDCAtItsOwnRegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("OIDC request to %s, want none", r.URL.Path)
	}))
	defer srv.Close()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL+"/idtoken")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "reqtok")
	t.Setenv("NPM_ID_TOKEN", "")
	t.Setenv("NPM_CONFIG_USERCONFIG", "")

	repo, pkg := workspace(t, map[string]string{".npmrc": "@acme:registry=https://npm.acme.example/"})
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	var env []string
	was := npmPublish
	npmPublish = func(_ context.Context, _ string, e []string, _ ...string) (string, string, error) {
		env = e
		return "", "", nil
	}
	t.Cleanup(func() { npmPublish = was })

	resp, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: pkg, OIDC: true})
	if err != nil || !resp.Published || !strings.Contains(resp.Message, "OIDC skipped") {
		t.Fatalf("resp = %+v, err = %v, want published with OIDC skipped", resp, err)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "NPM_CONFIG_USERCONFIG=") && kv != "NPM_CONFIG_USERCONFIG=" {
			t.Errorf("npm publish got %s, want npm's own auth", kv)
		}
	}
}

// publishConfig.registry is expanded as an .npmrc value is: a set variable is its value, an unset or empty one is an
// error naming the manifest — never the literal placeholder passed to npm.
func TestNpmRegistryExpandsPublishConfig(t *testing.T) {
	manifest := `{"name":"@acme/lib","version":"1.2.0","publishConfig":{"registry":"${ACME_PUBLISH_REGISTRY}"}}`
	t.Setenv("ACME_PUBLISH_REGISTRY", "https://npm.acme.example/")
	repo, pkg := workspace(t, map[string]string{"packages/lib/package.json": manifest})
	if got, err := npmRegistry(repo, pkg, ""); err != nil || got != "https://npm.acme.example/" {
		t.Errorf("set: npmRegistry = %q, %v", got, err)
	}
	t.Setenv("ACME_PUBLISH_REGISTRY", "")
	if got, err := npmRegistry(repo, pkg, ""); err == nil || !strings.Contains(err.Error(), "package.json") {
		t.Errorf("empty: npmRegistry = %q, %v, want an error naming package.json", got, err)
	}
	os.Unsetenv("ACME_PUBLISH_REGISTRY")
	if got, err := npmRegistry(repo, pkg, ""); err == nil || !strings.Contains(err.Error(), "not set") {
		t.Errorf("unset: npmRegistry = %q, %v, want a not-set error", got, err)
	}
}
