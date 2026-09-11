package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/account"
	"github.com/rigsmith/rigsmith/internal/clauderig/dirmap"
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
	if got.Session != account.SessionOK {
		t.Errorf("session = %q, want ok — a success never carries any other state", got.Session)
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
		got := decodePrepare(t, out)
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
		got := decodePrepare(t, out)
		if got.Prepared || got.Reason != prepareNoTokens {
			t.Errorf("reason = %q prepared=%v, want %q", got.Reason, got.Prepared, prepareNoTokens)
		}
		if got.ConfigDir != "" {
			t.Errorf("a refusal must not hand back a directory to launch under, got %q", got.ConfigDir)
		}
	})
}

// --no-share means "don't link anything in", not "unlink what an earlier shared
// run linked" — the same contract as `run --no-share`, and what the help and
// docs now say. Pinned so the wording and the behaviour cannot drift apart.
func TestPrepareNoShareKeepsLinksAnEarlierSharedRunMade(t *testing.T) {
	st := prepareFixture(t)
	link := filepath.Join(st.ConfigDir("w-x-com"), "settings.json")

	if _, errOut, err := runPrepareCmd(t, "w@x.com"); err != nil {
		t.Fatalf("shared prepare: %v\nstderr: %s", err, errOut)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("shared prepare did not link settings.json: %v", err)
	}

	out, errOut, err := runPrepareCmd(t, "w@x.com", "--no-share", "--json")
	if err != nil {
		t.Fatalf("no-share replay: %v\nstderr: %s", err, errOut)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("--no-share removed a link an earlier shared run made; the contract is that it is kept: %v", err)
	}
	// Decoded as *bool on purpose: an ABSENT field also reads as false through
	// the real struct, and absent is exactly the contract breach to catch.
	var got struct {
		Shared *bool `json:"shared"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if got.Shared == nil {
		t.Error("shared must be present under --no-share")
	} else if *got.Shared {
		t.Error("shared should report the mode requested — false under --no-share — even though earlier links remain")
	}
}

// A bare `prepare` in a mapped directory prints the mapped note — and in plain
// mode that note must not land on stdout, or `$(clauderig account prepare)`
// captures two lines. Pinned because the first cut did exactly that.
func TestPrepareMappedDirectoryKeepsStdoutToTheDir(t *testing.T) {
	st := prepareFixture(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dm, err := dirmapStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dm.Set(cwd, func(e *dirmap.Entry) { e.Account = "w-x-com" }); err != nil {
		t.Fatal(err)
	}

	out, errOut, err := runPrepareCmd(t)
	if err != nil {
		t.Fatalf("prepare: %v\nstderr: %s", err, errOut)
	}
	if strings.TrimSpace(out) != st.ConfigDir("w-x-com") {
		t.Errorf("stdout = %q, want ONLY the config dir", out)
	}
	if !strings.Contains(errOut, "mapped:") {
		t.Errorf("the mapped note belongs on stderr, got %q", errOut)
	}
}

func TestPrepareNamesTheRightRefusalForResolution(t *testing.T) {
	st := prepareFixture(t)
	// A second account sharing the domain makes "x.com" ambiguous.
	cred, _ := json.Marshal(map[string]any{
		"claudeAiOauth":    map[string]any{"accessToken": "acc-v", "refreshToken": "ref-v", "subscriptionType": "max"},
		"organizationUuid": "org-v",
	})
	oauth, _ := json.Marshal(map[string]any{"emailAddress": "v@x.com", "organizationUuid": "org-v"})
	if _, _, err := st.CaptureLive(cred, oauth); err != nil {
		t.Fatal(err)
	}

	t.Run("ambiguous reference is not a miss", func(t *testing.T) {
		out, _, err := runPrepareCmd(t, "x.com", "--json")
		if err == nil {
			t.Fatal("an ambiguous reference was prepared")
		}
		got := decodePrepare(t, out)
		if got.Reason != prepareAmbiguous {
			t.Errorf("reason = %q, want %q — the fix is to be more specific, not to add an account", got.Reason, prepareAmbiguous)
		}
	})

	t.Run("a mapping to a vanished account is not unmapped", func(t *testing.T) {
		cwd, _ := os.Getwd()
		dm, _ := dirmapStore()
		if _, err := dm.Set(cwd, func(e *dirmap.Entry) { e.Account = "ghost" }); err != nil {
			t.Fatal(err)
		}
		out, _, err := runPrepareCmd(t, "--json")
		if err == nil {
			t.Fatal("a broken mapping was silently prepared")
		}
		got := decodePrepare(t, out)
		if got.Reason != prepareFailed {
			t.Errorf("reason = %q, want %q — sending the user to `account map` for a directory that IS mapped is the wrong advice", got.Reason, prepareFailed)
		}
		if !strings.Contains(got.Message, "ghost") {
			t.Errorf("message should name the mapped account, got %q", got.Message)
		}
	})
}

// A profile is keyed by an account, but `/login` as someone else inside it
// leaves the first account's name on the directory and the second account's
// token in it. EnsureSession rightly never clobbers a live token, so only
// prepare can catch this — and a launcher recording "this ran as X" must.
func TestPrepareRefusesAProfileReLoggedAsAnotherAccount(t *testing.T) {
	st := prepareFixture(t)
	dir := st.ConfigDir("w-x-com")
	other, _ := json.Marshal(map[string]any{
		"claudeAiOauth":    map[string]any{"accessToken": "acc-o", "refreshToken": "ref-o"},
		"organizationUuid": "org-someone-else",
	})
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), other, 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, err := runPrepareCmd(t, "w@x.com", "--json")
	if err == nil {
		t.Fatal("a profile authenticating as another organization was handed to the launcher under this account's name")
	}
	got := decodePrepare(t, out)
	if got.Reason != prepareProfileDesync {
		t.Errorf("reason = %q, want %q", got.Reason, prepareProfileDesync)
	}
	if got.ConfigDir != "" {
		t.Errorf("a desynced profile must not be handed back, got %q", got.ConfigDir)
	}
	if !strings.Contains(got.Message, "org-someone-else") {
		t.Errorf("message should name the organization the profile actually holds, got %q", got.Message)
	}
}

// On macOS the per-profile Keychain entry carries no organization, so the
// credential half of the identity check is absent on every migrated profile.
// The profile's own .claude.json → oauthAccount is the half that IS there, and
// it is what `/login` rewrites — so it must catch the re-login on its own.
func TestPrepareRefusesWhenTheProfileIdentityBlockNamesAnotherAccount(t *testing.T) {
	st := prepareFixture(t)
	dir := st.ConfigDir("w-x-com")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The credential says nothing about who it is (the Keychain shape).
	cred, _ := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "acc-w", "refreshToken": "ref-w"}})
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), cred, 0o600); err != nil {
		t.Fatal(err)
	}
	block := `{"numStartups":3,"oauthAccount":{"emailAddress":"someone@else.com","organizationUuid":"org-else"}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(block), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, err := runPrepareCmd(t, "w@x.com", "--json")
	if err == nil {
		t.Fatal("a profile whose identity block names another account was handed to the launcher")
	}
	got := decodePrepare(t, out)
	if got.Reason != prepareProfileDesync {
		t.Errorf("reason = %q, want %q", got.Reason, prepareProfileDesync)
	}
	if !strings.Contains(got.Message, "someone@else.com") {
		t.Errorf("message should name who the profile is logged in as, got %q", got.Message)
	}

	// And a block that AGREES is not a refusal — the check must not fire on
	// the normal case where the credential carries no org at all.
	agree := `{"oauthAccount":{"emailAddress":"w@x.com","organizationUuid":"org-w"}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(agree), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := runPrepareCmd(t, "w@x.com", "--json"); err != nil {
		t.Fatalf("an agreeing identity block was refused: %v\nstderr: %s", err, errOut)
	}
}

// A credential file that exists but cannot be read is "unknown", not "no
// tokens": treating it as absent would let EnsureSession seed over a credential
// it never saw.
func TestPrepareReportsAnUnreadableProfileCredentialAsUnknown(t *testing.T) {
	st := prepareFixture(t)
	dir := st.ConfigDir("w-x-com")
	// A directory where the file should be: exists, and ReadFile fails on
	// every platform without needing to fiddle with permissions.
	if err := os.MkdirAll(filepath.Join(dir, ".credentials.json"), 0o700); err != nil {
		t.Fatal(err)
	}

	out, _, err := runPrepareCmd(t, "w@x.com", "--json")
	if err == nil {
		t.Fatal("an unreadable profile credential was prepared over")
	}
	got := decodePrepare(t, out)
	if got.Reason != prepareSessionUnknown {
		t.Errorf("reason = %q, want %q", got.Reason, prepareSessionUnknown)
	}
}

func TestClassifyPrepareResolveOnlyCallsAMissAMiss(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"unmapped":    {errUnmappedDirectory, prepareUnmapped},
		"no such":     {wrap(account.ErrNoSuchAccount), prepareNoSuchAccount},
		"no accounts": {account.ErrNoAccounts, prepareNoSuchAccount},
		"ambiguous":   {wrap(account.ErrAmbiguousRef), prepareAmbiguous},
		"getwd/other": {os.ErrPermission, prepareFailed},
		"broken map":  {wrap(os.ErrClosed), prepareFailed},
	}
	for name, c := range cases {
		if got := classifyPrepareResolve(c.err); got != c.want {
			t.Errorf("%s: classify = %q, want %q", name, got, c.want)
		}
	}
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

// decodePrepare reads the one object stdout must carry, failing the test on
// anything else — a refusal that is not JSON is a broken contract, not a
// refusal, and `_ = json.Unmarshal` would have let it pass as one.
func decodePrepare(t *testing.T, out string) prepareJSON {
	t.Helper()
	var got prepareJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out)
	}
	return got
}
