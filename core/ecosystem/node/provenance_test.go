package node

import (
	"context"
	"errors"
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
	if slices.Contains(*args, "--provenance") {
		t.Errorf("npm publish args = %v, want no --provenance", *args)
	}
}

// always fails the publish, before npm runs, when it can't attest.
func TestProvenanceAlwaysFailsWithoutAnIDToken(t *testing.T) {
	noProvenanceEnv(t)
	repo, pkg := workspace(t, nil)
	stubNpmView(t, "", "npm error code E404", errors.New("exit status 1"))
	stubNpmProvenance(t, true)
	args, _ := stubPublish(t)
	_, err := (&Adapter{}).Publish(context.Background(), plugin.PublishRequest{RepoRoot: repo, Package: pkg, Provenance: "always"})
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
