package velopack

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSetupKeychainRealSecurityE2E runs setupKeychain against the real macOS
// `security` tool with a throwaway self-signed cert. It creates and deletes a
// keychain under a temp dir, so it is skipped unless VELOPACK_KEYCHAIN_E2E=1:
//
//	VELOPACK_KEYCHAIN_E2E=1 go test ./core/ecosystem/velopack/ -run TestSetupKeychainRealSecurityE2E -v
//
// A self-signed cert is never a *valid* codesigning identity, so setup runs
// create, settings, unlock, import and key access for real, then stops at the
// identity check — which is the point: it proves those commands work as built
// and that a failed setup leaves no keychain and an unchanged search list.
// Signing and notarizing for real needs a Developer ID cert and is left to a
// real release.
func TestSetupKeychainRealSecurityE2E(t *testing.T) {
	if os.Getenv("VELOPACK_KEYCHAIN_E2E") != "1" {
		t.Skip("set VELOPACK_KEYCHAIN_E2E=1 to run against the real macOS security tool")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	const cn = "shiprig keychain e2e"
	dir := t.TempDir()
	// LibreSSL, not a Homebrew OpenSSL 3+: its .p12 uses the legacy encryption
	// `security import` reads.
	openssl := func(args ...string) {
		if out, err := exec.Command("/usr/bin/openssl", args...).CombinedOutput(); err != nil {
			t.Fatalf("openssl %v: %v\n%s", args, err, out)
		}
	}
	key, cert, p12 := filepath.Join(dir, "key.pem"), filepath.Join(dir, "cert.pem"), filepath.Join(dir, "cert.p12")
	openssl("req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", key, "-out", cert, "-days", "1",
		"-subj", "/CN="+cn, "-addext", "extendedKeyUsage=codeSigning", "-addext", "keyUsage=digitalSignature")
	openssl("pkcs12", "-export", "-inkey", key, "-in", cert, "-out", p12, "-passout", "pass:e2e-pass")
	der, err := os.ReadFile(p12)
	if err != nil {
		t.Fatal(err)
	}

	searchList := func() string {
		out, err := exec.Command("security", "list-keychains", "-d", "user").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	before := searchList()

	var kc, listedAfterCreate, allIdentities string
	ctx := context.Background()
	run := func(name string, args ...string) (string, error) {
		stdout, _, err := runCmdEnv(ctx, "", os.Environ(), name, args...)
		switch args[0] {
		case "create-keychain":
			kc = args[len(args)-1]
			listedAfterCreate = searchList()
		case "find-identity":
			// Without -v: the imported identity, valid or not.
			all, _, _ := runCmdEnv(ctx, "", os.Environ(), "security", "find-identity", "-p", "codesigning", kc)
			allIdentities = all
		}
		return stdout, err
	}
	env := []string{
		envSignP12 + "=" + base64.StdEncoding.EncodeToString(der),
		envSignPassword + "=e2e-pass",
	}
	_, cleanup, err := setupKeychain(Macos{SignIdentity: cn}, env, run)
	cleanup()

	if err == nil || !strings.Contains(err.Error(), "is not a valid codesigning identity") {
		t.Fatalf("want setup to get through import and stop at the identity check, got %v", err)
	}
	t.Logf("identity check (expected for a self-signed cert): %v", err)
	if !strings.Contains(allIdentities, cn) {
		t.Errorf("the cert should have been imported into the keychain; find-identity listed:\n%s", allIdentities)
	}
	t.Logf("create-keychain added it to the search list: %v", strings.Contains(listedAfterCreate, filepath.Base(kc)))
	if _, err := os.Stat(kc); !os.IsNotExist(err) {
		t.Errorf("the keychain %s must be deleted after a failed setup", kc)
	}
	if after := searchList(); after != before {
		t.Errorf("the user search list must end as it started\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
