package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/codexrig/account"
)

// These are contracts: other programs shell out to them. The store had tests;
// the command surface — what lands on stdout, in which shape, with which
// stable codes — had none, so a routing or serialisation change could break a
// launcher with every test green.

func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := NewAccountCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errb.String(), err
}

func TestAccountListJSONHasTheStableShape(t *testing.T) {
	twoAccounts(t)
	stdout, _, err := run(t, "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout)
	}
	for _, k := range []string{"active", "desynced", "accounts"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("top-level %q missing", k)
		}
	}
	accts, _ := doc["accounts"].([]any)
	if len(accts) != 2 {
		t.Fatalf("accounts = %v, want two", accts)
	}
	row, _ := accts[0].(map[string]any)
	for _, k := range []string{"id", "email", "active", "disabled", "credentialTokens", "session"} {
		if _, ok := row[k]; !ok {
			t.Errorf("account row lacks %q: %v", k, row)
		}
	}
}

func TestPrepareJSONRefusesWithAStableCodeOnStdout(t *testing.T) {
	twoAccounts(t) // two enabled, nothing bound: nothing to pick
	cmd := NewAccountPrepareCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("prepare picked an account it had no grounds to pick")
	}
	var doc prepareJSON
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%q", err, out.String())
	}
	if doc.Prepared || doc.Reason != reasonUnmapped || doc.Message == "" {
		t.Errorf("refusal object = %+v, want prepared=false with the unmapped-directory code and a message", doc)
	}
	if strings.Contains(out.String(), "prepared:") {
		t.Error("prose leaked onto stdout")
	}
}

func TestPrepareRefusesAHomeThatIsAnotherAccounts(t *testing.T) {
	s, _ := twoAccounts(t)
	alice, err := s.Resolve("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	home, err := s.EnsureHome(alice, true)
	if err != nil {
		t.Fatal(err)
	}
	// Somebody logged bob in inside alice's home.
	bobCred := fakeCred(t, "bob@other.test", "acct-b")
	if err := os.WriteFile(filepath.Join(home, "auth.json"), bobCred, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := NewAccountPrepareCmd()
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs([]string{"--json", "alice@example.com"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("prepare certified a home that authenticates as somebody else")
	}
	var doc prepareJSON
	_ = json.Unmarshal(out.Bytes(), &doc)
	if doc.Reason != reasonHomeDesync {
		t.Errorf("reason = %q, want %q", doc.Reason, reasonHomeDesync)
	}
	// And run refuses the same home rather than launching under alice's label.
	if _, _, err := run(t, "run", "alice@example.com"); err == nil || !strings.Contains(err.Error(), "wrong label") {
		t.Errorf("run did not refuse: %v", err)
	}
}

// CODEX_HOME has to reach the child, and only the child.
func TestRunHandsTheChildTheAccountsHomeAndLeavesTheParentAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake codex is a shell script")
	}
	s, home := twoAccounts(t)
	alice, err := s.Resolve("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	seen := filepath.Join(bin, "seen.env")
	script := "#!/bin/sh\nprintf '%s\\n' \"$CODEX_HOME\" > " + seen + "\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODEX_HOME", "")
	os.Unsetenv("CODEX_HOME")

	if _, _, err := run(t, "run", "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the fake codex never ran: %v", err)
	}
	want := s.HomeDir(alice.ID)
	if strings.TrimSpace(string(got)) != want {
		t.Errorf("child saw CODEX_HOME=%q, want %q", strings.TrimSpace(string(got)), want)
	}
	if v, set := os.LookupEnv("CODEX_HOME"); set {
		t.Errorf("the parent's environment gained CODEX_HOME=%q", v)
	}
	_ = home
}

func TestRunRefusesExtraArgumentsBeforeTheDash(t *testing.T) {
	twoAccounts(t)
	if _, _, err := run(t, "run", "alice@example.com", "extra", "--", "--help"); err == nil || !strings.Contains(err.Error(), "one account reference") {
		t.Errorf("an argument between the reference and -- was dropped silently: %v", err)
	}
	_ = account.ErrNoAccounts
}
