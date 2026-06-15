package sign

import (
	"context"
	"testing"
)

// collectMasker records every value handed to it, so tests can assert secrets are
// registered for redaction.
type collectMasker struct{ got []string }

func (m *collectMasker) Add(v string) { m.got = append(m.got, v) }

// TestResolveEnvEmpty: no refs → nil, nothing resolved.
func TestResolveEnvEmpty(t *testing.T) {
	env, err := ResolveEnv(context.Background(), nil, nil)
	if err != nil || env != nil {
		t.Fatalf("ResolveEnv(nil) = %v, %v; want nil, nil", env, err)
	}
}

// TestResolveEnvResolvesAndMasks: env: refs resolve from the environment and each
// value is registered with the masker.
func TestResolveEnvResolvesAndMasks(t *testing.T) {
	t.Setenv("MY_CERT", "cert-data")
	t.Setenv("MY_PW", "hunter2")
	m := &collectMasker{}

	env, err := ResolveEnv(context.Background(), map[string]string{
		"CSC_LINK":         "env:MY_CERT",
		"CSC_KEY_PASSWORD": "env:MY_PW",
	}, m)
	if err != nil {
		t.Fatal(err)
	}
	if env["CSC_LINK"] != "cert-data" || env["CSC_KEY_PASSWORD"] != "hunter2" {
		t.Errorf("resolved env = %v", env)
	}
	masked := map[string]bool{}
	for _, v := range m.got {
		masked[v] = true
	}
	if !masked["cert-data"] || !masked["hunter2"] {
		t.Errorf("secrets not all masked: %v", m.got)
	}
}

// TestResolveEnvMissingFails: an unset reference fails the whole resolution, so a
// misconfigured signing setup is surfaced rather than silently unsigned.
func TestResolveEnvMissingFails(t *testing.T) {
	_, err := ResolveEnv(context.Background(), map[string]string{"CSC_LINK": "env:DEFINITELY_UNSET_VAR"}, nil)
	if err == nil {
		t.Fatal("expected an error for an unset signing reference")
	}
}
