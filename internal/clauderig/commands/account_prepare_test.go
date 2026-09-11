package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/account"
)

// prepareFixture points HOME at a temp dir (so DefaultStore and ClaudeHome both
// land there), seeds one customization to share, and stores one account with a
// healthy credential. Returns the store, so a test can break things on purpose.
func prepareFixture(t *testing.T) *account.Store {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// A profile of the machine-wide login would make EnsureSession's Keychain
	// probe consult the real store; keep the test's view of "the default
	// profile" inside the sandbox too.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := account.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	cred, _ := json.Marshal(map[string]any{
		"claudeAiOauth":    map[string]any{"accessToken": "acc-w", "refreshToken": "ref-w", "subscriptionType": "max"},
		"organizationUuid": "org-w",
	})
	oauth, _ := json.Marshal(map[string]any{"emailAddress": "w@x.com", "organizationUuid": "org-w"})
	if _, _, err := st.CaptureLive(cred, oauth); err != nil {
		t.Fatal(err)
	}
	return st
}

// runPrepareCmd executes `account prepare <args>` and returns stdout, stderr
// and the error, the three things a launcher looks at.
func runPrepareCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newAccountPrepareCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestPrepareJSONHandsBackAReadyConfigDir(t *testing.T) {
	st := prepareFixture(t)

	out, errOut, err := runPrepareCmd(t, "w@x.com", "--json")
	if err != nil {
		t.Fatalf("prepare: %v\nstderr: %s", err, errOut)
	}

	var got prepareJSON
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", jerr, out)
	}
	if !got.Prepared || got.Reason != "" {
		t.Fatalf("prepared=%v reason=%q, want a clean success: %+v", got.Prepared, got.Reason, got)
	}
	if got.ID != "w-x-com" || got.Email != "w@x.com" {
		t.Errorf("identity = %q/%q, want w-x-com/w@x.com", got.ID, got.Email)
	}
	if got.ConfigDir != st.ConfigDir("w-x-com") {
		t.Errorf("configDir = %q, want the store's profile dir %q", got.ConfigDir, st.ConfigDir("w-x-com"))
	}
	// The dir is READY, not merely named: the credential was seeded and the
	// shared customization linked in — the two things a launcher building the
	// path by hand would not get.
	if _, serr := os.Stat(filepath.Join(got.ConfigDir, ".credentials.json")); serr != nil {
		t.Errorf("profile credential not seeded: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(got.ConfigDir, "settings.json")); serr != nil {
		t.Errorf("shared settings.json not linked in: %v", serr)
	}
	if !got.Shared {
		t.Error("shared should be true by default")
	}
	if got.Session != account.SessionOK && got.Session != account.SessionUnknown {
		t.Errorf("session = %q, want ok (or unknown where the Keychain is unreadable)", got.Session)
	}
	// One object on stdout, nothing else — a launcher must not have to strip prose.
	if strings.Count(strings.TrimSpace(out), "\n{") != 0 || !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("stdout carries more than the object:\n%s", out)
	}
}

func TestPrepareWithoutJSONPrintsOnlyTheDirOnStdout(t *testing.T) {
	st := prepareFixture(t)

	out, errOut, err := runPrepareCmd(t, "w@x.com", "--no-share")
	if err != nil {
		t.Fatalf("prepare: %v\nstderr: %s", err, errOut)
	}
	// `export CLAUDE_CONFIG_DIR=$(clauderig account prepare w)` is the whole
	// shell integration, so stdout is the bare value and the title is a note.
	if strings.TrimSpace(out) != st.ConfigDir("w-x-com") {
		t.Errorf("stdout = %q, want the bare config dir", out)
	}
	if !strings.Contains(errOut, "prepared:") {
		t.Errorf("stderr should carry the note, got %q", errOut)
	}
	if _, serr := os.Lstat(filepath.Join(st.ConfigDir("w-x-com"), "settings.json")); serr == nil {
		t.Error("--no-share still linked settings.json in")
	}
}

func TestPrepareRefusesWithAStableReason(t *testing.T) {
	st := prepareFixture(t)

	t.Run("unknown account", func(t *testing.T) {
		out, _, err := runPrepareCmd(t, "nobody", "--json")
		if err == nil {
			t.Fatal("an unknown reference was prepared")
		}
		var got prepareJSON
		if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
			t.Fatalf("a refusal must still emit the object: %v\n%s", jerr, out)
		}
		if got.Prepared || got.Reason != prepareNoSuchAccount {
			t.Errorf("reason = %q prepared=%v, want %q", got.Reason, got.Prepared, prepareNoSuchAccount)
		}
	})

	t.Run("no account and no mapping", func(t *testing.T) {
		out, _, err := runPrepareCmd(t, "--json")
		if err == nil {
			t.Fatal("an unmapped directory was silently prepared as some account")
		}
		var got prepareJSON
		_ = json.Unmarshal([]byte(out), &got)
		if got.Reason != prepareUnmapped {
			t.Errorf("reason = %q, want %q", got.Reason, prepareUnmapped)
		}
		if !strings.Contains(got.Message, "clauderig account prepare") {
			t.Errorf("message should name THIS command in its advice, got %q", got.Message)
		}
	})

	t.Run("stored credential has no tokens", func(t *testing.T) {
		// A token-less stored credential with no seeded profile is the case a
		// launcher most needs to know about: the fix is a fresh `account add`,
		// and nothing it can do with the directory will make it authenticate.
		// Written straight to disk: SaveCredential refuses a token-less blob
		// (that guard is the point of it), so the state has to be produced the
		// way it arises for real — a login that expired after it was stored.
		blank, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{}, "organizationUuid": "org-w"})
		if err := os.WriteFile(filepath.Join(st.Root, "accounts", "w-x-com", "credential.json"), blank, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(st.ConfigDir("w-x-com")); err != nil {
			t.Fatal(err)
		}
		out, _, err := runPrepareCmd(t, "w@x.com", "--json")
		if err == nil {
			t.Fatal("a profile that cannot authenticate was reported as prepared")
		}
		var got prepareJSON
		_ = json.Unmarshal([]byte(out), &got)
		if got.Prepared || got.Reason != prepareNoTokens {
			t.Errorf("reason = %q prepared=%v, want %q", got.Reason, got.Prepared, prepareNoTokens)
		}
		if got.ConfigDir != "" {
			t.Errorf("a refusal must not hand back a directory to launch under, got %q", got.ConfigDir)
		}
	})
}

// The classifier is sentinel-based on purpose — prose changes must not
// silently turn a known failure into "failed".
func TestClassifyPrepareFailureUsesSentinels(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"no tokens":        {account.ErrStoredNoTokens, prepareNoTokens},
		"unreadable":       {account.ErrSessionUnreadable, prepareSessionUnknown},
		"something else":   {os.ErrPermission, prepareFailed},
		"wrapped sentinel": {wrap(account.ErrStoredNoTokens), prepareNoTokens},
	}
	for name, c := range cases {
		if got := classifyPrepareFailure(c.err); got != c.want {
			t.Errorf("%s: classify = %q, want %q", name, got, c.want)
		}
	}
}

func wrap(err error) error { return &wrapped{err} }

type wrapped struct{ err error }

func (w *wrapped) Error() string { return "wrapped: " + w.err.Error() }
func (w *wrapped) Unwrap() error { return w.err }
