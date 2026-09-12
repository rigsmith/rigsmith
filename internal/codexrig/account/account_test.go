package account

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
)

// Every credential in this file is synthetic: the JWT is unsigned, the tokens
// are sequential placeholders, and nothing here reads or writes a real
// ~/.codex. The point of a fixture builder rather than a literal is that the
// identity claims and the token fields have to move together — a test that
// hard-codes one and forgets the other passes against a bug.

func fakeJWT(t *testing.T, email, name, acct, plan string) string {
	t.Helper()
	body := map[string]any{
		"email": email,
		"name":  name,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": acct,
			"chatgpt_plan_type":  plan,
		},
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." + enc.EncodeToString(b) + ".signature-is-not-verified"
}

func fakeCred(t *testing.T, email, name, acct, plan string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{
			"id_token":      fakeJWT(t, email, name, acct, plan),
			"access_token":  "fake-access-0001",
			"refresh_token": "fake-refresh-0001",
			"account_id":    acct,
		},
		"last_refresh": "2026-09-11T00:00:00.000000Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// sandbox points HOME at a temp dir, so the machine's real Codex home is never
// read and never written, and returns a store rooted beside it.
func sandbox(t *testing.T) (*Store, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows' equivalent, for os.UserHomeDir
	t.Setenv(codexhome.EnvHome, "")
	codexHome := filepath.Join(home, codexhome.DirName)
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	s := &Store{
		Root: filepath.Join(home, ".codexrig"),
		scan: func(string) ([]Instance, error) { return nil, nil },
	}
	return s, codexHome
}

func TestIdentityComesFromTheCredentialItself(t *testing.T) {
	id := IdentityOf(fakeCred(t, "alice@example.com", "Alice", "acct-1", "pro"))
	if id.Email != "alice@example.com" || id.Name != "Alice" || id.PlanType != "pro" || id.AccountID != "acct-1" {
		t.Fatalf("identity = %+v; every field should come from the id_token's claims", id)
	}
	if id.AuthMode != AuthModeChatGPT {
		t.Errorf("AuthMode = %q, want %q", id.AuthMode, AuthModeChatGPT)
	}
}

func TestIdentityOfACredentialItCannotParseIsEmptyNotAPanic(t *testing.T) {
	// A Codex upgrade that reshapes auth.json must degrade to "I don't know who
	// this is", never to a crash in a tool whose job is to keep the file safe.
	for _, raw := range []string{"", "{", "null", `{"tokens":{"id_token":"not-a-jwt"}}`} {
		got := IdentityOf([]byte(raw))
		if got.Email != "" {
			t.Errorf("IdentityOf(%q) invented an email %q", raw, got.Email)
		}
	}
}

func TestHasTokensSeparatesLoggedInFromLoggedOut(t *testing.T) {
	cases := map[string]bool{
		`{"tokens":{"refresh_token":"r"}}`:                  true,
		`{"tokens":{"access_token":"a"}}`:                   true,
		`{"OPENAI_API_KEY":"sk-fake"}`:                      true,
		`{"tokens":{"refresh_token":"","access_token":""}}`: false,
		`{"tokens":null,"OPENAI_API_KEY":null}`:             false,
		`{}`:                                                false,
		``:                                                  false,
	}
	for raw, want := range cases {
		if got := HasTokens([]byte(raw)); got != want {
			t.Errorf("HasTokens(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestCaptureLiveNamesByEmailAndReusesTheIDOnRecapture(t *testing.T) {
	s, _ := sandbox(t)
	a, existed, err := s.CaptureLive(fakeCred(t, "alice@example.com", "Alice", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	if existed {
		t.Error("the first capture is not an update")
	}
	if a.ID != "alice-example-com" {
		t.Fatalf("id = %q, want the slugified email", a.ID)
	}

	// Naming must survive a token rotation: Codex rotates the refresh token on
	// every refresh, so an id derived from a token would mint a new account
	// several times a day.
	again, existed, err := s.CaptureLive(fakeCred(t, "alice@example.com", "Alice", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	if !existed || again.ID != a.ID {
		t.Fatalf("recapture gave id %q existed=%v, want the same account back", again.ID, existed)
	}
}

func TestCaptureLiveCarriesForwardWhatBelongsToTheAccount(t *testing.T) {
	s, _ := sandbox(t)
	a, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "Alice", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetAlias(a.ID, "work"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisabled(a.ID, true); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "Alice", "acct-1", "pro")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Resolve(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Alias != "work" || !got.Disabled {
		t.Errorf("alias/disabled were lost on recapture: %+v — they belong to the account, not to the credential", got)
	}
}

func TestCaptureLiveSuffixesOnlyARealCollision(t *testing.T) {
	s, _ := sandbox(t)
	first, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "Alice", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	// Same email, DIFFERENT Codex account: a real case, and the one place a
	// suffix is correct.
	second, existed, err := s.CaptureLive(fakeCred(t, "alice@example.com", "Alice", "acct-2", "plus"))
	if err != nil {
		t.Fatal(err)
	}
	if existed {
		t.Error("a different account id is a new account, not an update")
	}
	if second.ID == first.ID {
		t.Fatalf("both accounts got id %q — one would overwrite the other's credential", first.ID)
	}
	if second.ID != "alice-example-com-2" {
		t.Errorf("id = %q, want a numeric suffix on the slug", second.ID)
	}
}

func TestCaptureLiveRefusesACredentialItCannotName(t *testing.T) {
	s, _ := sandbox(t)
	// No id_token and no account id: nothing to file it under, and an account
	// codexrig cannot name is one it can never resolve again.
	_, _, err := s.CaptureLive([]byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-fake"}`))
	if err == nil {
		t.Fatal("expected a refusal for a credential with no identity at all")
	}
}

func TestCaptureLiveRefusesALoggedOutCredential(t *testing.T) {
	s, _ := sandbox(t)
	if _, _, err := s.CaptureLive([]byte(`{"auth_mode":"chatgpt","tokens":{"refresh_token":""}}`)); err == nil {
		t.Fatal("expected a refusal: capturing a logged-out file records an account that can never run")
	}
}

func TestResolveByIDAliasEmailAndSubstring(t *testing.T) {
	s, _ := sandbox(t)
	a, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "Alice", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CaptureLive(fakeCred(t, "bob@other.test", "Bob", "acct-2", "plus")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAlias(a.ID, "work"); err != nil {
		t.Fatal(err)
	}

	for _, ref := range []string{"alice-example-com", "work", "WORK", "alice@example.com", "alice"} {
		got, err := s.Resolve(ref)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", ref, err)
		}
		if got.ID != a.ID {
			t.Errorf("Resolve(%q) = %q, want %q", ref, got.ID, a.ID)
		}
	}
}

func TestResolveRefusesToGuessBetweenTwoMatches(t *testing.T) {
	s, _ := sandbox(t)
	if _, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-1", "pro")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CaptureLive(fakeCred(t, "alice@other.test", "A", "acct-2", "plus")); err != nil {
		t.Fatal(err)
	}
	// Picking one silently would switch the machine to the wrong login and
	// report success, which is the one outcome this store must not produce.
	if _, err := s.Resolve("alice"); !errors.Is(err, ErrAmbiguousRef) {
		t.Fatalf("Resolve(ambiguous) error = %v, want ErrAmbiguousRef", err)
	}
}

func TestSetAliasRefusesToShadowAnotherAccount(t *testing.T) {
	s, _ := sandbox(t)
	a, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.CaptureLive(fakeCred(t, "bob@other.test", "B", "acct-2", "plus"))
	if err != nil {
		t.Fatal(err)
	}
	// An alias equal to another account's id would silently redirect every
	// `switch <that id>` to this one.
	if err := s.SetAlias(a.ID, b.ID); err == nil {
		t.Fatal("expected a refusal: an alias must not shadow another account's id")
	}
	if err := s.SetAlias(a.ID, "Bob@other.test"); err == nil {
		t.Fatal("expected a refusal: an alias must not shadow another account's email, case-insensitively")
	}
}

func TestSaveCredentialRefusesABlobThatCannotAuthenticate(t *testing.T) {
	s, _ := sandbox(t)
	a, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	// Once the machine has switched away, the stored copy is the only one left.
	if err := s.SaveCredential(a.ID, []byte(`{"tokens":{"refresh_token":""}}`)); err == nil {
		t.Fatal("expected a refusal: this would destroy the last usable copy of the account")
	}
	if !s.CredentialHealthy(a.ID) {
		t.Error("the original credential should still be intact after the refusal")
	}
}

func TestEnsureHomeSeedsSharesAndReportsReady(t *testing.T) {
	s, codexHome := sandbox(t)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "AGENTS.md"), []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}

	home, err := s.EnsureHome(a, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.HomeStatus(a.ID); got != SessionOK {
		t.Fatalf("HomeStatus = %q, want %q", got, SessionOK)
	}
	// The credential must be the account's own copy, not a link back to the
	// machine's: sharing it would defeat the isolation entirely.
	st, err := os.Lstat(codexhome.Auth(home))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		t.Fatal("auth.json must be a real file in the account's home, never a link to the machine's")
	}
	// Windows maps only the read-only bit through os.Chmod, so 0600 is not
	// expressible there and the file reports -rw-rw-rw-. The production code is
	// right on POSIX; asserting it everywhere only ever failed the Windows leg.
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("auth.json mode = %v, want 0600 — it is the whole secret", st.Mode().Perm())
	}
	for _, name := range []string{"config.toml", "AGENTS.md"} {
		if _, err := os.Lstat(filepath.Join(home, name)); err != nil {
			t.Errorf("shared entry %s was not linked in: %v", name, err)
		}
	}
}

func TestEnsureHomeDoesNotReseedAHealthyHome(t *testing.T) {
	s, _ := sandbox(t)
	a, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	home, err := s.EnsureHome(a, false)
	if err != nil {
		t.Fatal(err)
	}
	// Stand in for Codex having refreshed its own tokens inside the home. The
	// stored copy is now OLDER, and re-seeding would throw the fresh one away.
	refreshed := fakeCred(t, "alice@example.com", "A", "acct-1", "pro")
	refreshed = []byte(strings.Replace(string(refreshed), "fake-refresh-0001", "fake-refresh-ROTATED", 1))
	if err := os.WriteFile(codexhome.Auth(home), refreshed, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.EnsureHome(a, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(codexhome.Auth(home))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "fake-refresh-ROTATED") {
		t.Error("EnsureHome overwrote a working home with the older stored copy")
	}
}

func TestANewCredentialMarksTheHomeStaleAndReseeds(t *testing.T) {
	s, _ := sandbox(t)
	a, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	home, err := s.EnsureHome(a, false)
	if err != nil {
		t.Fatal(err)
	}
	// A fresh capture of the same account. Nothing about the home's own files
	// changed, so without the stale marker the "it already authenticates" test
	// would keep handing back the previous login forever.
	updated := []byte(strings.Replace(string(fakeCred(t, "alice@example.com", "A", "acct-1", "pro")),
		"fake-access-0001", "fake-access-NEWER", 1))
	if _, _, err := s.CaptureLive(updated); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureHome(a, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(codexhome.Auth(home))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "fake-access-NEWER") {
		t.Error("a re-captured credential did not reach the isolated home")
	}
}

func TestSwitchRoundTripsTheDisplacedLogin(t *testing.T) {
	s, codexHome := sandbox(t)
	alice := fakeCred(t, "alice@example.com", "A", "acct-1", "pro")
	if err := os.WriteFile(codexhome.Auth(codexHome), alice, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CaptureLive(alice); err != nil {
		t.Fatal(err)
	}

	// Bob logs in on the machine, and is captured.
	bob := fakeCred(t, "bob@other.test", "B", "acct-2", "plus")
	if err := os.WriteFile(codexhome.Auth(codexHome), bob, 0o600); err != nil {
		t.Fatal(err)
	}
	bobAcct, _, err := s.CaptureLive(bob)
	if err != nil {
		t.Fatal(err)
	}

	// Bob's token then rotates, the way Codex rotates it on every refresh.
	rotated := []byte(strings.Replace(string(bob), "fake-refresh-0001", "fake-refresh-ROTATED", 1))
	if err := os.WriteFile(codexhome.Auth(codexHome), rotated, 0o600); err != nil {
		t.Fatal(err)
	}

	aliceAcct, err := s.Resolve("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Switch(aliceAcct, SwitchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Switched || res.From != bobAcct.ID {
		t.Fatalf("switch result = %+v, want a completed swap displacing %s", res, bobAcct.ID)
	}
	if res.Backup == "" {
		t.Error("the displaced credential should have been backed up")
	}

	live, err := ReadLive()
	if err != nil {
		t.Fatal(err)
	}
	if IdentityOf(live).Email != "alice@example.com" {
		t.Fatal("the machine is not logged in as the account that was switched to")
	}
	// The whole reason the swap stores the displaced copy back: Bob's ROTATED
	// token has to survive, not the one captured before the refresh.
	stored, err := s.Credential(bobAcct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), "fake-refresh-ROTATED") {
		t.Error("the displaced login was stored back at its pre-refresh token — switching away and back would log it out")
	}
}

func TestSwitchRefusesWhileCodexIsRunning(t *testing.T) {
	s, codexHome := sandbox(t)
	alice := fakeCred(t, "alice@example.com", "A", "acct-1", "pro")
	if err := os.WriteFile(codexhome.Auth(codexHome), alice, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CaptureLive(alice); err != nil {
		t.Fatal(err)
	}
	bob := fakeCred(t, "bob@other.test", "B", "acct-2", "plus")
	if err := os.WriteFile(codexhome.Auth(codexHome), bob, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CaptureLive(bob); err != nil {
		t.Fatal(err)
	}

	s.scan = func(string) ([]Instance, error) {
		return []Instance{{PID: 4242, Kind: "codex", Source: "process"}}, nil
	}
	aliceAcct, err := s.Resolve("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Switch(aliceAcct, SwitchOptions{})
	if !errors.Is(err, ErrCodexBusy) {
		t.Fatalf("error = %v, want ErrCodexBusy", err)
	}
	if len(res.Blocking) != 1 || res.Blocking[0].PID != 4242 {
		t.Errorf("Blocking = %+v, want the live session named so the caller can show it", res.Blocking)
	}
	live, err := ReadLive()
	if err != nil {
		t.Fatal(err)
	}
	if IdentityOf(live).Email != "bob@other.test" {
		t.Fatal("a refused switch changed the live credential anyway")
	}
}

func TestSwitchNeverFailsOpenOnAnUnreadableProcessTable(t *testing.T) {
	// "I could not look" is not "nothing is running". Treating it as one is how
	// a swap lands under a live session.
	s, codexHome := sandbox(t)
	alice := fakeCred(t, "alice@example.com", "A", "acct-1", "pro")
	if err := os.WriteFile(codexhome.Auth(codexHome), alice, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CaptureLive(alice); err != nil {
		t.Fatal(err)
	}
	s.scan = func(string) ([]Instance, error) { return nil, ErrProcessScan }

	a, err := s.Resolve("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Switch(a, SwitchOptions{}); !errors.Is(err, ErrProcessScan) {
		t.Fatalf("error = %v, want ErrProcessScan", err)
	}
}

func TestSwitchRefusesWhenTheEnvironmentPointsElsewhere(t *testing.T) {
	s, codexHome := sandbox(t)
	alice := fakeCred(t, "alice@example.com", "A", "acct-1", "pro")
	if err := os.WriteFile(codexhome.Auth(codexHome), alice, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CaptureLive(alice); err != nil {
		t.Fatal(err)
	}
	// A shell inside `account run` is looking at a different Codex than the one
	// a machine-wide swap would change.
	t.Setenv(codexhome.EnvHome, filepath.Join(t.TempDir(), "somewhere-else"))

	a, err := s.Resolve("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Switch(a, SwitchOptions{}); err == nil {
		t.Fatal("expected a refusal while CODEX_HOME points away from the machine's home")
	}
}

func TestDiagnoseReportsAPointerThatNamesSomebodyElse(t *testing.T) {
	s, codexHome := sandbox(t)
	alice := fakeCred(t, "alice@example.com", "A", "acct-1", "pro")
	bob := fakeCred(t, "bob@other.test", "B", "acct-2", "plus")
	if err := os.WriteFile(codexhome.Auth(codexHome), alice, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CaptureLive(alice); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexhome.Auth(codexHome), bob, 0o600); err != nil {
		t.Fatal(err)
	}
	bobAcct, _, err := s.CaptureLive(bob)
	if err != nil {
		t.Fatal(err)
	}
	// Put the machine back on alice's credential WITHOUT moving the pointer —
	// what a hand-run `codex login` does.
	if err := os.WriteFile(codexhome.Auth(codexHome), alice, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(bobAcct.ID); err != nil {
		t.Fatal(err)
	}

	o := s.Diagnose()
	if o.InSync {
		t.Fatal("Diagnose called this in sync; the pointer names bob and the credential is alice's")
	}
	if len(o.Problems()) == 0 {
		t.Fatal("a disagreement with no problem reported is a doctor that cannot fail")
	}
	// The credential is the authority: everything a user sees should name the
	// account their requests actually authenticate as.
	if id, _ := s.LiveAccount(); id != "alice-example-com" {
		t.Errorf("LiveAccount = %q, want the account the credential names", id)
	}
}

func TestSlugifyProducesIDsThatConsumersAccept(t *testing.T) {
	// The ids travel: an outside caller reads them out of meta.json to launch
	// under an account, and refuses anything that is not lowercase
	// alphanumerics with single internal dashes.
	cases := map[string]string{
		"Alice@Example.COM":  "alice-example-com",
		"  bob@x.test  ":     "bob-x-test",
		"a..b@c":             "a-b-c",
		"UPPER_Case-Mixed@x": "upper-case-mixed-x",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	for _, id := range []string{"alice-example-com", "upper-case-mixed-x"} {
		if strings.HasPrefix(id, "-") || strings.HasSuffix(id, "-") || strings.Contains(id, "--") {
			t.Errorf("id %q would be rejected downstream", id)
		}
	}
}

// os.WriteFile applies its mode only when it CREATES the file, so rewriting an
// auth.json that was somehow left world-readable would faithfully preserve that.
// The store writes a fresh file and renames, which cannot inherit a mode.
func TestSaveTightensAWorldReadableCredential(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go's Chmod on Windows maps only the read-only bit, so 0600 is not expressible")
	}
	s, _ := sandbox(t)
	a, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	cred := s.credPath(a.ID)
	if err := os.Chmod(cred, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-1", "pro")); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(cred)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v after a rewrite, want 0600", st.Mode().Perm())
	}
}

// An accounts directory that cannot be listed but can still be written to is
// the shape that loses a credential: capture sees no accounts, treats every
// slug as free, and overwrites somebody else's login with this one.
func TestCaptureRefusesWhenItCannotSeeWhatIsAlreadyThere(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits do not gate traversal on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	s, _ := sandbox(t)
	first, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.credPath(first.ID))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(s.accountsDir(), 0o300); err != nil { // writable, not listable
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(s.accountsDir(), 0o700) })

	if _, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-9", "pro")); err == nil {
		t.Fatal("capture proceeded without being able to see the existing accounts")
	}
	_ = os.Chmod(s.accountsDir(), 0o700)
	after, err := os.ReadFile(s.credPath(first.ID))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("the existing account's credential was overwritten")
	}
}

// One email address can hold several ChatGPT accounts, so email alone is not
// identity: filing B's credential under A overwrites the only stored copy of A's.
func TestCaptureFromHomeRefusesADifferentAccountOnTheSameEmail(t *testing.T) {
	s, _ := sandbox(t)
	a, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	home, err := s.EnsureHome(a, true)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Credential(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Same person, second account.
	if err := writeAuthAt(home, fakeCred(t, "alice@example.com", "A", "acct-2", "pro")); err != nil {
		t.Fatal(err)
	}
	err = s.CaptureFromHome(a)
	if err == nil {
		t.Fatal("a different account's credential was filed under this one")
	}
	if !strings.Contains(err.Error(), "acct-2") {
		t.Errorf("the refusal does not name what it found: %v", err)
	}
	after, _ := s.Credential(a.ID)
	if string(after) != string(before) {
		t.Error("the stored credential was overwritten anyway")
	}
}

// The documented repair path: log in inside the account's own home, then
// capture. Pins the success return and that the stored credential is the
// refreshed one, not the one from before the login.
func TestCaptureFromHomeStoresTheRefreshedCredential(t *testing.T) {
	s, _ := sandbox(t)
	a, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	home, err := s.EnsureHome(a, true)
	if err != nil {
		t.Fatal(err)
	}
	refreshed := fakeCred(t, "alice@example.com", "A", "acct-1", "plus") // same login, new tokens/plan
	if err := writeAuthAt(home, refreshed); err != nil {
		t.Fatal(err)
	}
	if err := s.CaptureFromHome(a); err != nil {
		t.Fatalf("capture from the account's own home failed: %v", err)
	}
	got, err := s.Credential(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(refreshed) {
		t.Error("the stored credential is not the one captured from the home")
	}
}

// active.json naming an account whose record is gone used to pass straight
// through as PointerID when the live credential was absent, and the diagnosis
// called it healthy.
func TestDiagnoseReportsAPointerAtAMissingAccount(t *testing.T) {
	s, _ := sandbox(t)
	a, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(s.dir(a.ID)); err != nil { // the record is gone, the pointer is not
		t.Fatal(err)
	}
	obs := s.Diagnose()
	if obs.PointerStale != a.ID {
		t.Errorf("PointerStale = %q, want %q", obs.PointerStale, a.ID)
	}
	if len(obs.Problems()) == 0 || obs.InSync {
		t.Errorf("a pointer at a missing account was reported healthy: %+v", obs)
	}
}

// A meta file that exists and cannot be read is an account with a problem, not
// a directory to skip — skipping it made the account vanish from every listing.
func TestListReportsAnUnreadableAccountRatherThanHidingIt(t *testing.T) {
	s, _ := sandbox(t)
	a, _, err := s.CaptureLive(fakeCred(t, "alice@example.com", "A", "acct-1", "pro"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.metaPath(a.ID), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); err == nil || !strings.Contains(err.Error(), a.ID) {
		t.Fatalf("List = %v, want an error naming the account whose record is damaged", err)
	}
}
