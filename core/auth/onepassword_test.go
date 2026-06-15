package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestClassifyOpError(t *testing.T) {
	runErr := errors.New("exit status 1")
	cases := []struct {
		name   string
		stderr string
		want   string // substring the friendly error must contain
	}{
		{"signed out", "[ERROR] you are not currently signed in. Please run `op signin`.", "op signin"},
		{"no account", "no account found for this session", "op signin"},
		{"service account", "expected OP_SERVICE_ACCOUNT_TOKEN to be set", "op signin"},
		{"not an item", `"op://Vault/Item/field" isn't an item in the "Vault" vault`, "not found"},
		{"no vault", "no vault matching \"Nope\" found", "not found"},
		{"generic", "some unexpected failure", "some unexpected failure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := classifyOpError("op://Vault/Item/field", tc.stderr, runErr)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("classifyOpError(%q) = %v, want substring %q", tc.stderr, err, tc.want)
			}
		})
	}
}

func TestPreflightRef_Env(t *testing.T) {
	t.Setenv("SHIPRIG_PF", "x")
	if d, ok := PreflightRef(context.Background(), "env:SHIPRIG_PF"); !ok || !strings.Contains(d, "is set") {
		t.Errorf("set env: (%q, %v)", d, ok)
	}
	if d, ok := PreflightRef(context.Background(), "env:SHIPRIG_PF_UNSET"); ok || !strings.Contains(d, "not set") {
		t.Errorf("unset env: (%q, %v)", d, ok)
	}
}

func TestPreflightRef_CmdAndUnknown(t *testing.T) {
	if _, ok := PreflightRef(context.Background(), "cmd:echo hi"); !ok {
		t.Error("cmd: should preflight ok")
	}
	if _, ok := PreflightRef(context.Background(), "weird-ref"); ok {
		t.Error("unknown scheme should not be ok")
	}
}

func TestPreflightRef_OpNotInstalled(t *testing.T) {
	// Empty PATH makes the `op` lookup fail deterministically, regardless of
	// whether op is installed on the machine running the test.
	t.Setenv("PATH", "")
	d, ok := PreflightRef(context.Background(), "op://Vault/Item/field")
	if ok || !strings.Contains(d, "not found") {
		t.Errorf("op not installed: (%q, %v)", d, ok)
	}
}

func TestResolveOpRef_NotInstalled(t *testing.T) {
	t.Setenv("PATH", "")
	if _, err := resolveOpRef(context.Background(), "op://Vault/Item/field"); err == nil ||
		!strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("want not-found error, got %v", err)
	}
}
