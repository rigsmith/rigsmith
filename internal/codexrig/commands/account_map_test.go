package commands

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/agentrig/dirmap"
	"github.com/rigsmith/rigsmith/internal/codexrig/account"
	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
)

// fakeCred builds a synthetic credential. The JWT is unsigned and the tokens are
// obvious placeholders; nothing here reads a real ~/.codex.
func fakeCred(t *testing.T, email, acct string) []byte {
	t.Helper()
	claims, err := json.Marshal(map[string]any{
		"email": email,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": acct, "chatgpt_plan_type": "pro",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	b, err := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"id_token":      enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString(claims) + ".sig",
			"refresh_token": "fake-refresh-0001",
			"account_id":    acct,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// twoAccounts stands up a sandbox home with two tracked logins, and returns the
// store plus the home.
func twoAccounts(t *testing.T) (*account.Store, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(codexhome.EnvHome, "")
	if err := os.MkdirAll(filepath.Join(home, codexhome.DirName), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &account.Store{Root: filepath.Join(home, ".codexrig")}
	for _, a := range []struct{ email, acct string }{
		{"alice@example.com", "acct-a"}, {"bob@other.test", "acct-b"},
	} {
		if _, _, err := s.CaptureLive(fakeCred(t, a.email, a.acct)); err != nil {
			t.Fatal(err)
		}
	}
	return s, home
}

func bind(t *testing.T, home, dir, id string) {
	t.Helper()
	store := dirmap.New(filepath.Join(home, ".codexrig", "dir-map.json"))
	if _, err := store.Set(dir, func(e *dirmap.Entry) { e.Account = id }); err != nil {
		t.Fatal(err)
	}
}

func TestABareReferenceRefusesWhenSeveralAccountsAreUnbound(t *testing.T) {
	s, home := twoAccounts(t)
	t.Chdir(home)
	_, err := resolveAccountRef(s, "")
	if !errors.Is(err, account.ErrUnmapped) {
		t.Fatalf("error = %v, want ErrUnmapped — running the wrong login is worse than being asked which", err)
	}
	// The code is what a launcher branches on, so it must not collapse into the
	// generic failure.
	if got := classifyResolve(err); got != reasonUnmapped {
		t.Errorf("reason = %q, want %q", got, reasonUnmapped)
	}
}

func TestADirectoryBindingAnswersABareReference(t *testing.T) {
	s, home := twoAccounts(t)
	proj := filepath.Join(home, "Git", "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	bind(t, home, proj, "alice-example-com")

	t.Chdir(proj)
	got, err := resolveAccountRef(s, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "alice-example-com" {
		t.Errorf("resolved %q, want the bound account", got.ID)
	}
}

func TestABindingCoversEverythingBeneathIt(t *testing.T) {
	// The case that makes this worth having: one binding on a repository covers
	// every worktree under it.
	s, home := twoAccounts(t)
	proj := filepath.Join(home, "Git", "proj")
	deep := filepath.Join(proj, "worktrees", "feature")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	bind(t, home, proj, "alice-example-com")

	t.Chdir(deep)
	got, err := resolveAccountRef(s, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "alice-example-com" {
		t.Errorf("resolved %q from a directory beneath the binding", got.ID)
	}
}

func TestTheNearestBindingWins(t *testing.T) {
	s, home := twoAccounts(t)
	proj := filepath.Join(home, "Git", "proj")
	inner := filepath.Join(proj, "vendor")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	bind(t, home, proj, "alice-example-com")
	bind(t, home, inner, "bob-other-test")

	t.Chdir(inner)
	got, err := resolveAccountRef(s, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "bob-other-test" {
		t.Errorf("resolved %q, want the more specific binding", got.ID)
	}
}

func TestASiblingDirectoryIsNotCovered(t *testing.T) {
	// The separator check: /a/proj must not cover /a/proj-other.
	s, home := twoAccounts(t)
	proj := filepath.Join(home, "Git", "proj")
	sibling := filepath.Join(home, "Git", "proj-other")
	for _, d := range []string{proj, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bind(t, home, proj, "alice-example-com")

	t.Chdir(sibling)
	if _, err := resolveAccountRef(s, ""); !errors.Is(err, account.ErrUnmapped) {
		t.Fatalf("error = %v — a sibling directory was covered by the binding", err)
	}
}

func TestAnExplicitReferenceStillOutranksTheBinding(t *testing.T) {
	s, home := twoAccounts(t)
	proj := filepath.Join(home, "Git", "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	bind(t, home, proj, "alice-example-com")

	t.Chdir(proj)
	got, err := resolveAccountRef(s, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "bob-other-test" {
		t.Errorf("resolved %q, want the account that was named", got.ID)
	}
}

func TestASingleAccountNeedsNoBinding(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(codexhome.EnvHome, "")
	if err := os.MkdirAll(filepath.Join(home, codexhome.DirName), 0o700); err != nil {
		t.Fatal(err)
	}
	s := &account.Store{Root: filepath.Join(home, ".codexrig")}
	if _, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "acct-a")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	got, err := resolveAccountRef(s, "")
	if err != nil {
		t.Fatalf("a single account should need no binding: %v", err)
	}
	if got.ID != "alice-example-com" {
		t.Errorf("resolved %q", got.ID)
	}
}

func TestABindingToADeletedAccountDoesNotResolve(t *testing.T) {
	// Forgetting an account prunes what pointed at it; a stale binding that
	// still resolved would launch the wrong login.
	s, home := twoAccounts(t)
	proj := filepath.Join(home, "Git", "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	bind(t, home, proj, "alice-example-com")
	if err := s.Remove("alice-example-com"); err != nil {
		t.Fatal(err)
	}

	t.Chdir(proj)
	got, err := resolveAccountRef(s, "")
	if err == nil && got.ID == "alice-example-com" {
		t.Fatal("a binding to a forgotten account still resolved to it")
	}
	// One account left, so it falls through to that rather than refusing.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != "bob-other-test" {
		t.Errorf("resolved %q", got.ID)
	}
}

// An unreadable mapping is not an absent one. Treating both as "" let the
// caller fall through to "there happens to be only one enabled account" and
// start Codex under a login this directory was explicitly bound away from.
func TestAnUnreadableBindingRefusesRatherThanGuessing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits do not gate reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: permissions are not enforced")
	}
	s, home := twoAccounts(t)
	proj := filepath.Join(home, "Git", "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	bind(t, home, proj, "alice-example-com")
	// Exactly ONE account left enabled, so falling through would SUCCEED and
	// silently hand back bob — the hazard itself. With two enabled, the
	// fall-through errors as ambiguous and the test passes for the wrong
	// reason, which it did until this line was added.
	if err := s.SetDisabled("alice-example-com", true); err != nil {
		t.Fatal(err)
	}

	store, err := dirMap()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store.Path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(store.Path, 0o600) })

	t.Chdir(proj)
	got, err := resolveAccountRef(s, "")
	if err == nil {
		t.Fatalf("an unreadable mapping resolved to %q instead of refusing", got.ID)
	}
	if !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("refused for a different reason than the unreadable mapping: %v", err)
	}
}
