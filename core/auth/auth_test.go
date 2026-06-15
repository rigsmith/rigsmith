package auth

import (
	"context"
	"testing"
)

func TestResolve_EnvScheme(t *testing.T) {
	t.Setenv("SHIPRIG_TEST_TOKEN", "  tok-env  ")
	got, err := Resolve(context.Background(), Request{Ref: "env:SHIPRIG_TEST_TOKEN"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Token != "tok-env" {
		t.Errorf("token = %q, want trimmed %q", got.Token, "tok-env")
	}
	if got.Method != MethodEnv {
		t.Errorf("method = %q, want %q", got.Method, MethodEnv)
	}
}

func TestResolve_FallbackEnv(t *testing.T) {
	t.Setenv("NPM_TOKEN", "tok-fallback")
	got, err := Resolve(context.Background(), Request{FallbackEnv: "NPM_TOKEN"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.Resolved() || got.Token != "tok-fallback" || got.Method != MethodEnv {
		t.Errorf("got %+v, want env token tok-fallback", got)
	}
}

func TestResolve_RefBeatsFallback(t *testing.T) {
	t.Setenv("SHIPRIG_REF", "from-ref")
	t.Setenv("NPM_TOKEN", "from-fallback")
	got, err := Resolve(context.Background(), Request{Ref: "env:SHIPRIG_REF", FallbackEnv: "NPM_TOKEN"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Token != "from-ref" {
		t.Errorf("token = %q, want ref to win with %q", got.Token, "from-ref")
	}
}

func TestResolve_None(t *testing.T) {
	got, err := Resolve(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Resolved() || got.Method != MethodNone {
		t.Errorf("got %+v, want unresolved none", got)
	}
}

func TestResolve_CmdScheme(t *testing.T) {
	got, err := Resolve(context.Background(), Request{Ref: "cmd:printf tok-cmd"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Token != "tok-cmd" || got.Method != MethodRef {
		t.Errorf("got %+v, want secret-ref tok-cmd", got)
	}
}

func TestResolve_UnknownScheme(t *testing.T) {
	if _, err := Resolve(context.Background(), Request{Ref: "what://nope"}); err == nil {
		t.Fatal("want error for unrecognized scheme, got nil")
	}
}

func TestResolve_EmptyRefToken(t *testing.T) {
	// env: scheme pointing at an unset var resolves to empty → an error, since a
	// configured ref that yields nothing is a misconfiguration, not "use ambient".
	if _, err := Resolve(context.Background(), Request{Ref: "env:SHIPRIG_DEFINITELY_UNSET"}); err == nil {
		t.Fatal("want error for empty resolved token, got nil")
	}
}

func TestResolve_RegistersWithMasker(t *testing.T) {
	t.Setenv("SHIPRIG_SECRET", "s3cr3t-value")
	r := NewRedactor()
	if _, err := Resolve(context.Background(), Request{Ref: "env:SHIPRIG_SECRET", Masker: r}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := r.Redact("token is s3cr3t-value here"); got != "token is *** here" {
		t.Errorf("Redact = %q, want masked", got)
	}
}
