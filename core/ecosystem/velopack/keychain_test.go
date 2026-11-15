package velopack

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeKeychain records the commands setupKeychain runs, answers find-identity
// with identities, and fails the command named by failOn (echoing its argv, as
// runCmdEnv does, so redaction is exercised).
type fakeKeychain struct {
	cmds       [][]string
	identities string
	failOn     string
	certBytes  []byte // what the .p12 file held when `security import` ran
	keyBytes   []byte // what the .p8 file held when store-credentials ran
}

func (f *fakeKeychain) run(name string, args ...string) (string, error) {
	cmd := append([]string{name}, args...)
	f.cmds = append(f.cmds, cmd)
	verb := args[0]
	if name == "xcrun" {
		verb = args[1]
	}
	if verb == f.failOn {
		return "", errors.New(name + " " + strings.Join(args, " ") + ": exit status 1")
	}
	switch verb {
	case "import":
		f.certBytes, _ = os.ReadFile(args[1])
	case "store-credentials":
		f.keyBytes, _ = os.ReadFile(flagValue(args, "--key"))
	case "find-identity":
		return f.identities, nil
	}
	return "", nil
}

func (f *fakeKeychain) verbs() []string {
	var out []string
	for _, c := range f.cmds {
		if c[0] == "xcrun" {
			out = append(out, c[2])
		} else {
			out = append(out, c[1])
		}
	}
	return out
}

func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

const testIdentity = "Developer ID Application: Acme (TEAM123456)"

func keychainEnv(extra ...string) []string {
	return append([]string{
		"PATH=/usr/bin",
		envSignP12 + "=" + base64.StdEncoding.EncodeToString([]byte("p12-bytes")),
		envSignPassword + "=hunter2-p12",
	}, extra...)
}

func notaryEnv() []string {
	return keychainEnv(
		envNotaryKey+"="+base64.StdEncoding.EncodeToString([]byte("notary-p8-bytes")),
		envNotaryKeyID+"=KEYID123",
		envNotaryIssuer+"=issuer-uuid",
	)
}

func signedMacos() Macos {
	return Macos{SignIdentity: testIdentity, NotaryProfile: "acme-notary"}
}

// TestSetupKeychainWithoutCertIsANoop: no MACOS_SIGN_P12 means the login
// keychain is used as before — nothing is run and no path comes back.
func TestSetupKeychainWithoutCertIsANoop(t *testing.T) {
	f := &fakeKeychain{}
	kc, cleanup, err := setupKeychain(signedMacos(), []string{"PATH=/usr/bin"}, f.run)
	cleanup()
	if err != nil || kc != "" || len(f.cmds) != 0 {
		t.Errorf("no cert in env: got kc=%q err=%v cmds=%v, want nothing", kc, err, f.cmds)
	}
}

// TestSetupKeychainImportsCertAndStoresNotaryProfile walks the whole sequence:
// the keychain is created, the decoded cert imported for codesign, the identity
// checked, and the notary profile stored in that keychain; cleanup deletes it.
func TestSetupKeychainImportsCertAndStoresNotaryProfile(t *testing.T) {
	f := &fakeKeychain{identities: `  1) ABCDEF "` + testIdentity + `"` + "\n     1 valid identities found"}
	kc, cleanup, err := setupKeychain(signedMacos(), notaryEnv(), f.run)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"create-keychain", "set-keychain-settings", "unlock-keychain", "import", "set-key-partition-list", "find-identity", "store-credentials"}
	if got := f.verbs(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("commands = %v, want %v", got, want)
	}
	if kc == "" || filepath.Ext(kc) != ".keychain-db" {
		t.Errorf("keychain path = %q", kc)
	}
	if string(f.certBytes) != "p12-bytes" {
		t.Errorf("imported cert = %q, want the decoded MACOS_SIGN_P12", f.certBytes)
	}
	if string(f.keyBytes) != "notary-p8-bytes" {
		t.Errorf("notary key file = %q, want the decoded MACOS_NOTARY_KEY", f.keyBytes)
	}
	imp := f.cmds[3]
	assertHas(t, imp, "-k", kc)
	assertHas(t, imp, "-P", "hunter2-p12")
	assertHas(t, imp, "-T", "/usr/bin/codesign")
	store := f.cmds[6]
	if store[2] != "store-credentials" || store[3] != "acme-notary" {
		t.Errorf("store-credentials must save under macos.notaryProfile, got %v", store)
	}
	assertHas(t, store, "--key-id", "KEYID123")
	assertHas(t, store, "--issuer", "issuer-uuid")
	assertHas(t, store, "--keychain", kc)
	// The base64 secrets travel as files, never as arguments.
	for _, c := range f.cmds {
		for _, a := range c {
			if a == envValue(notaryEnv(), envSignP12) || a == envValue(notaryEnv(), envNotaryKey) {
				t.Errorf("a base64 secret was passed as an argument: %v", c)
			}
		}
	}
	dir := filepath.Dir(kc)
	if _, err := os.Stat(filepath.Join(dir, "cert.p12")); !os.IsNotExist(err) {
		t.Error("the decoded .p12 must be removed right after import")
	}
	if _, err := os.Stat(filepath.Join(dir, "notary.p8")); !os.IsNotExist(err) {
		t.Error("the decoded .p8 must be removed right after store-credentials")
	}

	cleanup()
	last := f.cmds[len(f.cmds)-1]
	if last[1] != "delete-keychain" || last[2] != kc {
		t.Errorf("cleanup must delete the keychain, last command %v", last)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("cleanup must remove the temp dir %s", dir)
	}
}

// TestSetupKeychainAcceptsRawPEMNotaryKey: a .p8 stored as plain PEM text (not
// base64) is written to the key file as-is.
func TestSetupKeychainAcceptsRawPEMNotaryKey(t *testing.T) {
	f := &fakeKeychain{identities: `1) ABC "` + testIdentity + `"`}
	// Assembled at runtime so no PEM-shaped literal sits in the source: gitleaks
	// scans the whole history and would flag it as a private key.
	pem := "-----BEGIN " + "PRIVATE KEY-----\nfake\n-----END " + "PRIVATE KEY-----"
	env := keychainEnv(envNotaryKey+"="+pem, envNotaryKeyID+"=KEYID123")
	_, cleanup, err := setupKeychain(signedMacos(), env, f.run)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if string(f.keyBytes) != pem+"\n" {
		t.Errorf("notary key file = %q, want the PEM as given", f.keyBytes)
	}
}

// TestSetupKeychainCertOnlySkipsNotary: with no notary key the cert is still
// imported, and no profile is stored (notarization uses the login keychain's
// profile, or none).
func TestSetupKeychainCertOnlySkipsNotary(t *testing.T) {
	f := &fakeKeychain{identities: `1) ABC "` + testIdentity + `"`}
	m := signedMacos()
	m.NotaryProfile = ""
	_, cleanup, err := setupKeychain(m, keychainEnv(), f.run)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range f.verbs() {
		if v == "store-credentials" {
			t.Error("no MACOS_NOTARY_KEY, so no notary profile should be stored")
		}
	}
}

// TestSetupKeychainIdentityMissing: a cert that doesn't hold macos.signIdentity
// fails up front, naming what it does hold, and the keychain is still deleted.
func TestSetupKeychainIdentityMissing(t *testing.T) {
	f := &fakeKeychain{identities: `1) ABC "Developer ID Application: Someone Else (XYZ)"`}
	_, _, err := setupKeychain(signedMacos(), notaryEnv(), f.run)
	if err == nil || !strings.Contains(err.Error(), "Someone Else") {
		t.Fatalf("want an error listing the cert's identities, got %v", err)
	}
	if v := f.verbs(); v[len(v)-1] != "delete-keychain" {
		t.Errorf("a failed setup must delete the keychain, commands %v", v)
	}
}

// TestSetupKeychainFailureIsRedactedAndCleanedUp: a failing command's echoed
// argv carries the keychain and .p12 passwords; the returned error must not.
func TestSetupKeychainFailureIsRedactedAndCleanedUp(t *testing.T) {
	f := &fakeKeychain{failOn: "import"}
	_, _, err := setupKeychain(signedMacos(), notaryEnv(), f.run)
	if err == nil {
		t.Fatal("want the import failure")
	}
	keychainPass := flagValue(f.cmds[0][1:], "-p")
	if keychainPass == "" {
		t.Fatalf("create-keychain carried no -p password: %v", f.cmds[0])
	}
	for _, secret := range []string{"hunter2-p12", keychainPass} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaks a secret: %v", err)
		}
	}
	if !strings.Contains(err.Error(), "***") {
		t.Errorf("error should show where the secret was redacted: %v", err)
	}
	if v := f.verbs(); v[len(v)-1] != "delete-keychain" {
		t.Errorf("a failed setup must delete the keychain, commands %v", v)
	}
}

// TestSetupKeychainRejectsIncompleteConfig: each combination that would sign or
// notarize differently from what the env asks for is an error, before anything
// is created.
func TestSetupKeychainRejectsIncompleteConfig(t *testing.T) {
	notaryOnly := []string{envNotaryKey + "=a2V5", envNotaryKeyID + "=K"}
	noID := signedMacos()
	noID.SignIdentity = ""
	noProfile := signedMacos()
	noProfile.NotaryProfile = ""
	cases := []struct {
		name string
		m    Macos
		env  []string
		want string
	}{
		{"notary key without cert", signedMacos(), notaryOnly, envSignP12},
		{"cert without signIdentity", noID, keychainEnv(), "macos.signIdentity"},
		{"notary key without notaryProfile", noProfile, notaryEnv(), "macos.notaryProfile"},
		{"notary key without key id", signedMacos(), keychainEnv(envNotaryKey + "=a2V5"), envNotaryKeyID},
		{"cert not base64", signedMacos(), []string{envSignP12 + "=not base64!"}, "base64"},
	}
	for _, c := range cases {
		f := &fakeKeychain{}
		_, _, err := setupKeychain(c.m, c.env, f.run)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error naming %q, got %v", c.name, c.want, err)
		}
		if len(f.cmds) != 0 {
			t.Errorf("%s: nothing should run before the config is complete, ran %v", c.name, f.cmds)
		}
	}
}

// TestKeychainReachesVpkAndDmgSteps: once built, the keychain is handed to vpk
// (--keychain, for codesign and notarytool) and to the DMG's codesign and
// notarytool submit; a snapshot passes none of it.
func TestKeychainReachesVpkAndDmgSteps(t *testing.T) {
	cfg := fullCfg()
	cfg.Macos.keychain = "/tmp/k/signing.keychain-db"
	assertHas(t, buildPackArgs(cfg, "osx-arm64", "p", "o", "1.0.0", false, osMac, ""), "--keychain", cfg.Macos.keychain)
	if contains(buildPackArgs(cfg, "osx-arm64", "p", "o", "1.0.0", true, osMac, ""), "--keychain") {
		t.Error("a snapshot pack is unsigned and must not name a keychain")
	}

	steps := dmgFinishSteps(cfg.Macos, "x.dmg", false)
	if len(steps) < 2 {
		t.Fatalf("want sign + notarize steps, got %v", steps)
	}
	assertHas(t, steps[0].args, "--keychain", cfg.Macos.keychain)
	assertHas(t, steps[1].args, "--keychain", cfg.Macos.keychain)
	assertHas(t, steps[1].args, "--keychain-profile", "halyards-notary")
}

func TestPlansOS(t *testing.T) {
	if !plansOS([]string{"win-x64", "osx-arm64"}, osMac) || plansOS([]string{"win-x64"}, osMac) {
		t.Error("plansOS should report only a planned osx-* channel")
	}
}
