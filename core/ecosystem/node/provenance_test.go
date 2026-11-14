package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestProvenanceMode(t *testing.T) {
	for in, want := range map[string]string{"": "auto", "auto": "auto", " Always ": "always", "OFF": "off"} {
		if got, err := provenanceMode(in); err != nil || got != want {
			t.Errorf("provenanceMode(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	if _, err := provenanceMode("yes"); err == nil {
		t.Error(`provenanceMode("yes") succeeded, want an error`)
	}
}

func TestDecideProvenance(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        string
		idToken     bool
		npmOK       bool
		wantAttest  bool
		wantNote    string
		wantErr     string
		wantNpmAsks bool
	}{
		{name: "auto with an id-token", mode: "auto", idToken: true, npmOK: true, wantAttest: true, wantNote: " (provenance attested)", wantNpmAsks: true},
		{name: "auto without an id-token", mode: "auto"},
		{name: "auto with an old npm", mode: "auto", idToken: true, wantNote: " (provenance skipped — needs npm ≥ 9.5.0)", wantNpmAsks: true},
		{name: "always with an id-token", mode: "always", idToken: true, npmOK: true, wantAttest: true, wantNote: " (provenance attested)", wantNpmAsks: true},
		{name: "always without an id-token", mode: "always", npmOK: true, wantErr: "id-token: write"},
		{name: "always with an old npm", mode: "always", idToken: true, wantErr: "npm ≥ 9.5.0", wantNpmAsks: true},
		{name: "off with an id-token", mode: "off", idToken: true, npmOK: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := false
			attest, note, err := decideProvenance(tc.mode, tc.idToken, provenanceMinNpmToken, func() bool { asked = true; return tc.npmOK })
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if attest != tc.wantAttest || note != tc.wantNote {
				t.Errorf("attest, note = %v, %q; want %v, %q", attest, note, tc.wantAttest, tc.wantNote)
			}
			if asked != tc.wantNpmAsks {
				t.Errorf("npm asked = %v, want %v", asked, tc.wantNpmAsks)
			}
		})
	}
}

// stubNpmProvenance stands in for the npm version probe, recording the minimum
// it was asked about.
func stubNpmProvenance(t *testing.T, ok bool) *[3]int {
	t.Helper()
	var asked [3]int
	was := npmSupportsProvenance
	npmSupportsProvenance = func(_ context.Context, min [3]int) bool { asked = min; return ok }
	t.Cleanup(func() { npmSupportsProvenance = was })
	return &asked
}

func idTokenEnv(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "https://token.actions.example/?x=1")
	// A real Actions runner points this at its own event, whose repository
	// visibility would then decide; tests that want one set it.
	t.Setenv("GITHUB_EVENT_PATH", "")
}

// A token publish — every first publish of a new package — attests in GitHub
// Actions with an id-token. Before, only the OIDC path ever passed --provenance.
func TestTokenPublishAttestsProvenance(t *testing.T) {
	idTokenEnv(t)
	repo, pkg := workspace(t, nil)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	asked := stubNpmProvenance(t, true)
	args, _ := stubPublish(t)
	resp, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: repo, Package: pkg, Access: "public",
		Auth: &plugin.AuthCredential{Token: "npm_secret", Method: "env"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(*args, "--provenance") {
		t.Errorf("npm publish args = %v, want --provenance", *args)
	}
	if *asked != provenanceMinNpmToken {
		t.Errorf("npm version asked against %v, want %v", *asked, provenanceMinNpmToken)
	}
	if !strings.Contains(resp.Message, "provenance attested") {
		t.Errorf("message = %q", resp.Message)
	}
}

func TestProvenanceOffNeverAttests(t *testing.T) {
	idTokenEnv(t)
	repo, pkg := workspace(t, nil)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	stubNpmProvenance(t, true)
	args, _ := stubPublish(t)
	if _, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: repo, Package: pkg, Provenance: "off",
		Auth: &plugin.AuthCredential{Token: "npm_secret"},
	}); err != nil {
		t.Fatal(err)
	}
	// Leaving --provenance out isn't off: an .npmrc's provenance=true (or
	// NPM_CONFIG_PROVENANCE) would still have npm attest. The explicit flag
	// outranks both.
	if slices.Contains(*args, "--provenance") || !slices.Contains(*args, "--provenance=false") {
		t.Errorf("npm publish args = %v, want --provenance=false and no --provenance", *args)
	}
}

// Whenever auto decides not to attest — no id-token, for a public package
// or a restricted one — it says so to npm, so an .npmrc's provenance=true
// can't have npm attest anyway and fail the publish.
func TestProvenanceAutoNotAttestingOverridesNpmConfig(t *testing.T) {
	noProvenanceEnv(t)
	repo, pkg := workspace(t, nil)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	stubNpmProvenance(t, true)
	args, _ := stubPublish(t)
	for _, access := range []string{"public", "restricted"} {
		*args = nil
		if _, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: pkg, Access: access}); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(*args, "--provenance") || !slices.Contains(*args, "--provenance=false") {
			t.Errorf("access %s: npm publish args = %v, want --provenance=false and no --provenance", access, *args)
		}
	}
}

// always fails the publish, before npm runs, when it can't attest.
func TestProvenanceAlwaysFailsWithoutAnIDToken(t *testing.T) {
	noProvenanceEnv(t)
	repo, pkg := workspace(t, nil)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	stubNpmProvenance(t, true)
	args, _ := stubPublish(t)
	_, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: pkg, Access: "public", Provenance: "always"})
	if err == nil || !strings.Contains(err.Error(), "id-token") {
		t.Fatalf("err = %v, want a refusal naming the id-token", err)
	}
	if *args != nil {
		t.Errorf("npm publish ran with %v", *args)
	}
}

// A bad value is an error in a dry run too, not a silent auto.
func TestProvenanceRejectsAnUnknownValue(t *testing.T) {
	repo, pkg := workspace(t, nil)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	_, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: pkg, Provenance: "true", DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "node.provenance") {
		t.Errorf("err = %v, want one naming node.provenance", err)
	}
}

// npm attests only a package published with access public. auto skips any
// other, explicitly, so an .npmrc's provenance=true can't make npm refuse a
// publish that used to work; always fails with the reason.
func TestProvenanceSkipsARestrictedPackage(t *testing.T) {
	idTokenEnv(t)
	repo, pkg := workspace(t, nil)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	stubNpmAccess(t, "", "npm error code E404\nnpm error 404 Not Found", errors.New("exit status 1")) // a new package: the registry doesn't have it
	stubNpmProvenance(t, true)
	args, _ := stubPublish(t)
	resp, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: repo, Package: pkg, Access: "restricted",
		Auth: &plugin.AuthCredential{Token: "npm_secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(*args, "--provenance") || !slices.Contains(*args, "--provenance=false") {
		t.Errorf("npm publish args = %v, want --provenance=false and no --provenance", *args)
	}
	if !strings.Contains(resp.Message, "npm attests only public packages") {
		t.Errorf("message = %q, want it to say why provenance was skipped", resp.Message)
	}

	*args = nil
	_, err = (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: repo, Package: pkg, Access: "restricted", Provenance: "always",
		Auth: &plugin.AuthCredential{Token: "npm_secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "new or private") || !strings.Contains(err.Error(), "set access to public") {
		t.Fatalf("always: err = %v, want the new-or-private refusal", err)
	}
	if *args != nil {
		t.Errorf("npm publish ran with %v", *args)
	}
}

// stubNpmAccess stands in for `npm access get status`, recording its arguments.
func stubNpmAccess(t *testing.T, out, stderr string, err error) *[]string {
	t.Helper()
	var got []string
	was := npmAccessStatus
	npmAccessStatus = func(_ context.Context, _ string, _ []string, args ...string) (string, string, error) {
		got = args
		return out, stderr, err
	}
	t.Cleanup(func() { npmAccessStatus = was })
	return &got
}

// npm attests a package published with access other than public when the
// registry already holds it as public, so auto asks the registry rather than
// skipping it on the access setting alone.
func TestProvenanceAttestsAnAlreadyPublicPackageWhateverTheAccess(t *testing.T) {
	idTokenEnv(t)
	repo, pkg := workspace(t, nil)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	stubNpmProvenance(t, true)
	asked := stubNpmAccess(t, `{"@acme/lib": "public"}`, "", nil)
	args, _ := stubPublish(t)
	if _, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: repo, Package: pkg, Access: "restricted",
		Auth: &plugin.AuthCredential{Token: "npm_secret"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(*asked) == 0 || (*asked)[0] != "@acme/lib" {
		t.Errorf("visibility asked with %v, want the package named", *asked)
	}
	if !slices.Contains(*args, "--provenance") {
		t.Errorf("npm publish args = %v, want --provenance", *args)
	}
}

// A visibility lookup that fails (the registry unreachable, a refused token)
// says nothing about the package, so it isn't read as "new or private": it is
// asked once more, then auto skips the attestation naming the failure, and
// always fails with it.
func TestProvenanceReportsAFailedVisibilityLookup(t *testing.T) {
	idTokenEnv(t)
	repo, pkg := workspace(t, nil)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	stubNpmProvenance(t, true)
	calls := 0
	was := npmAccessStatus
	npmAccessStatus = func(context.Context, string, []string, ...string) (string, string, error) {
		calls++
		return "", "npm error code ECONNRESET", errors.New("exit status 1")
	}
	t.Cleanup(func() { npmAccessStatus = was })
	args, _ := stubPublish(t)

	resp, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: repo, Package: pkg, Access: "restricted",
		Auth: &plugin.AuthCredential{Token: "npm_secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("visibility asked %d times, want 2 (one retry)", calls)
	}
	if !slices.Contains(*args, "--provenance=false") {
		t.Errorf("npm publish args = %v, want --provenance=false", *args)
	}
	if !strings.Contains(resp.Message, "visibility lookup failed") || !strings.Contains(resp.Message, "ECONNRESET") {
		t.Errorf("message = %q, want the lookup failure and its cause", resp.Message)
	}

	*args = nil
	_, err = (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: repo, Package: pkg, Access: "restricted", Provenance: "always",
		Auth: &plugin.AuthCredential{Token: "npm_secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "visibility lookup failed") || !strings.Contains(err.Error(), "ECONNRESET") {
		t.Fatalf("always: err = %v, want the lookup failure and its cause", err)
	}
	if strings.Contains(err.Error(), "new or private") {
		t.Errorf("always: err = %v, says new or private for a failed lookup", err)
	}
	if *args != nil {
		t.Errorf("npm publish ran with %v", *args)
	}
}

// eventFile writes a GitHub Actions event payload naming the repository's
// visibility and points GITHUB_EVENT_PATH at it.
func eventFile(t *testing.T, payload string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_EVENT_PATH", path)
}

// npm's registry refuses a provenance statement from a private repository
// (HTTP 422), which npm doesn't check first: auto skips it, explicitly, and
// always fails before the upload.
func TestProvenanceSkipsAPrivateSourceRepository(t *testing.T) {
	idTokenEnv(t)
	repo, pkg := workspace(t, nil)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	stubNpmProvenance(t, true)
	args, _ := stubPublish(t)
	for _, payload := range []string{`{"repository": {"visibility": "private"}}`, `{"repository": {"private": true}}`} {
		eventFile(t, payload)
		*args = nil
		resp, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
			RepoRoot: repo, Package: pkg, Access: "public",
			Auth: &plugin.AuthCredential{Token: "npm_secret"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(*args, "--provenance") || !slices.Contains(*args, "--provenance=false") {
			t.Errorf("%s: npm publish args = %v, want --provenance=false and no --provenance", payload, *args)
		}
		if !strings.Contains(resp.Message, "only from a public repository") {
			t.Errorf("%s: message = %q, want it to say why provenance was skipped", payload, resp.Message)
		}
	}

	*args = nil
	_, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: repo, Package: pkg, Access: "public", Provenance: "always",
		Auth: &plugin.AuthCredential{Token: "npm_secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "repository is private") {
		t.Fatalf("always: err = %v, want the private-repository refusal", err)
	}
	if *args != nil {
		t.Errorf("npm publish ran with %v", *args)
	}

	// A public repository attests as before.
	eventFile(t, `{"repository": {"visibility": "public"}}`)
	*args = nil
	if _, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: repo, Package: pkg, Access: "public",
		Auth: &plugin.AuthCredential{Token: "npm_secret"},
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(*args, "--provenance") {
		t.Errorf("public repository: npm publish args = %v, want --provenance", *args)
	}
}

// A failed lookup's npm output can echo the registry URL with its
// credentials, and the auto note is printed as is on a successful publish:
// the credentials are masked before the failure reaches the note or the error.
func TestProvenanceLookupFailureRedactsCredentials(t *testing.T) {
	idTokenEnv(t)
	repo, pkg := workspace(t, nil)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	stubNpmProvenance(t, true)
	stubNpmAccess(t, "", "npm error code E500\nnpm error 500 Internal Server Error - GET https://ci:s3cr3t@npm.acme.dev/-/package/@acme%2flib/access\nAuthorization: Basic Y2k6czNjcjN0dG9rZW4=", errors.New("exit status 1"))
	stubPublish(t)

	resp, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: repo, Package: pkg, Access: "restricted",
		Auth: &plugin.AuthCredential{Token: "npm_secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: repo, Package: pkg, Access: "restricted", Provenance: "always",
		Auth: &plugin.AuthCredential{Token: "npm_secret"},
	})
	if err == nil {
		t.Fatal("always: published, want the lookup failure")
	}
	for name, text := range map[string]string{"auto message": resp.Message, "always error": err.Error()} {
		if strings.Contains(text, "s3cr3t") || strings.Contains(text, "Y2k6czNjcjN0dG9rZW4=") {
			t.Errorf("%s = %q, leaks the registry credentials", name, text)
		}
		if !strings.Contains(text, "https://***@npm.acme.dev") || !strings.Contains(text, "E500") {
			t.Errorf("%s = %q, want the failure with the URL masked", name, text)
		}
	}
}

// Only npm's own error code says the registry doesn't have the package: a
// 503 whose request URL happens to hold "E404" is a failed lookup, retried
// and reported, not a new or private package.
func TestProvenanceLookupMatchesNpmsErrorCodeNotAnyE404(t *testing.T) {
	idTokenEnv(t)
	repo, pkg := workspace(t, nil)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	stubNpmProvenance(t, true)
	calls := 0
	was := npmAccessStatus
	npmAccessStatus = func(context.Context, string, []string, ...string) (string, string, error) {
		calls++
		return "", "npm error code E503\nnpm error 503 Service Unavailable - GET https://npm.acme.dev/E404/-/package/@acme%2flib/access", errors.New("exit status 1")
	}
	t.Cleanup(func() { npmAccessStatus = was })
	stubPublish(t)

	_, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{
		RepoRoot: repo, Package: pkg, Access: "restricted", Provenance: "always",
		Auth: &plugin.AuthCredential{Token: "npm_secret"},
	})
	if err == nil || !strings.Contains(err.Error(), "visibility lookup failed") || strings.Contains(err.Error(), "new or private") {
		t.Fatalf("err = %v, want the lookup failure, not new or private", err)
	}
	if calls != 2 {
		t.Errorf("visibility asked %d times, want 2 (one retry)", calls)
	}
}
