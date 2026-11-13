package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/secrets"
)

const managedToken = "dp-release-token-never-printed"

type stubManager struct{ values map[string]string }

func (s stubManager) Name() string                                 { return "doppler" }
func (s stubManager) Title() string                                { return "Doppler" }
func (s stubManager) Ready(context.Context, string) error          { return nil }
func (s stubManager) Wrap(_ secrets.Scope, argv []string) []string { return argv }
func (s stubManager) Get(_ context.Context, _ string, _ secrets.Scope, name string) (string, error) {
	if v, ok := s.values[name]; ok {
		return v, nil
	}
	return "", secrets.ErrNotFound
}
func (s stubManager) All(context.Context, string, secrets.Scope) (map[string]string, error) {
	return s.values, nil
}

func managerCtx(t *testing.T, release string) context.Context {
	t.Helper()
	prev := secrets.InCI
	secrets.InCI = func() bool { return false }
	t.Cleanup(func() { secrets.InCI = prev })
	setup := &secrets.Setup{
		Provider: stubManager{values: map[string]string{"NPM_TOKEN": managedToken}},
		Settings: secrets.Settings{Project: "owlwatch", Release: release},
	}
	return secrets.WithReader(context.Background(), secrets.NewReader(setup, secrets.Release, "/repo"))
}

func TestDopplerRefReadsTheReleaseConfig(t *testing.T) {
	r := NewRedactor()
	cred, err := Resolve(managerCtx(t, "release"), Request{Ref: "doppler:NPM_TOKEN", Masker: r})
	if err != nil {
		t.Fatal(err)
	}
	if cred.Token != managedToken || cred.Method != MethodManager {
		t.Fatalf("cred = %+v", cred)
	}
	if cred.Source != "Doppler owlwatch/release (NPM_TOKEN)" {
		t.Errorf("Source = %q", cred.Source)
	}
	if got := r.Redact("publishing with " + managedToken); strings.Contains(got, managedToken) {
		t.Errorf("the token was not registered for redaction: %q", got)
	}
}

func TestDopplerRefFailsLoudly(t *testing.T) {
	cases := map[string]struct {
		ctx  context.Context
		ref  string
		want string
	}{
		"no doppler block":  {context.Background(), "doppler:NPM_TOKEN", `"doppler" block`},
		"no release config": {managerCtx(t, ""), "doppler:NPM_TOKEN", "doppler.release"},
		"missing secret":    {managerCtx(t, "release"), "doppler:FEEDZ_API_KEY", "not found"},
		"not a secret name": {managerCtx(t, "release"), "doppler:NPM TOKEN", "not a secret name"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cred, err := Resolve(c.ctx, Request{Ref: c.ref})
			if err == nil || cred.Token != "" {
				t.Fatalf("Resolve = %+v, %v; want an error and no token", cred, err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to mention %q", err, c.want)
			}
			if strings.Contains(err.Error(), managedToken) {
				t.Errorf("the error carries a value: %v", err)
			}
		})
	}
}

func TestSafeReasonKnowsDopplerRefs(t *testing.T) {
	if got := SafeReason("doppler:NPM_TOKEN", secrets.ErrNotFound); strings.Contains(got, "isn't one of") {
		t.Fatalf("SafeReason = %q; a doppler: ref is a known form", got)
	}
}
