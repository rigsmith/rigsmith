package velopack

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// Signing-env variables that carry macOS credentials for a machine whose login
// keychain holds neither the Developer ID cert nor a notary profile (a CI runner,
// a fresh Mac). Each can be a `doppler:NAME` reference in signing.env. The names
// match scripts/package-ui.sh, rigsmith's own release packaging.
const (
	envSignP12      = "MACOS_SIGN_P12"         // base64 of the Developer ID Application .p12
	envSignPassword = "MACOS_SIGN_PASSWORD"    // the .p12's password
	envNotaryKey    = "MACOS_NOTARY_KEY"       // the App Store Connect API key (.p8): PEM text, or base64 of it
	envNotaryKeyID  = "MACOS_NOTARY_KEY_ID"    // its key id
	envNotaryIssuer = "MACOS_NOTARY_ISSUER_ID" // its issuer id (empty for an individual key)
)

// keychainRun runs one command and returns its stdout. The error it returns may
// echo the command's arguments; setupKeychain redacts its secrets from it.
type keychainRun func(name string, args ...string) (string, error)

// setupKeychain builds a throwaway keychain from the signing env when it carries
// MACOS_SIGN_P12: the Developer ID cert imported for codesign, and, with
// MACOS_NOTARY_KEY, macos.notaryProfile stored in it for notarytool. It returns
// the keychain's path, which vpk (--keychain) and the DMG steps then use instead
// of the login keychain, and a cleanup that deletes it. With no MACOS_SIGN_P12
// it returns "" and a no-op, and the build signs from the login keychain as
// before. Every secret is kept out of argv where a file will do, and out of the
// errors it returns.
func setupKeychain(m Macos, env []string, run keychainRun) (string, func(), error) {
	noop := func() {}
	p12 := envValue(env, envSignP12)
	notaryKey := envValue(env, envNotaryKey)
	if strings.TrimSpace(p12) == "" {
		if strings.TrimSpace(notaryKey) != "" {
			return "", noop, fmt.Errorf("velopack: %s is set but %s is not — vpk reads the signing cert and the notary profile from one keychain, so both must come from the signing env", envNotaryKey, envSignP12)
		}
		return "", noop, nil
	}
	if strings.TrimSpace(m.SignIdentity) == "" {
		return "", noop, fmt.Errorf("velopack: %s is set but macos.signIdentity is empty — name the identity in the cert (e.g. \"Developer ID Application: Acme (TEAMID)\")", envSignP12)
	}
	notarize := strings.TrimSpace(notaryKey) != ""
	// vpk looks for the notary profile in the one keychain it is given, so a
	// profile kept only in the login keychain would fail at notarization, after
	// the whole build. Refuse it now instead.
	if !notarize && strings.TrimSpace(m.NotaryProfile) != "" {
		return "", noop, fmt.Errorf("velopack: %s is set and macos.notaryProfile names a profile, but %s is not — vpk reads the notary profile from the same temporary keychain as the cert, so supply the notary key too (or clear macos.notaryProfile to skip notarization)", envSignP12, envNotaryKey)
	}
	if notarize {
		if strings.TrimSpace(m.NotaryProfile) == "" {
			return "", noop, fmt.Errorf("velopack: %s is set but macos.notaryProfile is empty — set it to name the profile shiprig stores the key under", envNotaryKey)
		}
		if strings.TrimSpace(envValue(env, envNotaryKeyID)) == "" {
			return "", noop, fmt.Errorf("velopack: %s is set but %s is not", envNotaryKey, envNotaryKeyID)
		}
	}

	certDER, err := decodeBase64(p12)
	if err != nil {
		return "", noop, fmt.Errorf("velopack: %s is not valid base64: %w", envSignP12, err)
	}
	var keyPEM []byte
	if notarize {
		// The .p8 is text, so a secret store may hold it as-is rather than base64.
		if strings.HasPrefix(strings.TrimSpace(notaryKey), "-----BEGIN") {
			keyPEM = []byte(strings.TrimSpace(notaryKey) + "\n")
		} else if keyPEM, err = decodeBase64(notaryKey); err != nil {
			return "", noop, fmt.Errorf("velopack: %s is neither a PEM key nor valid base64: %w", envNotaryKey, err)
		}
	}

	pass, err := randomPassword()
	if err != nil {
		return "", noop, err
	}
	p12Pass := envValue(env, envSignPassword)
	secrets := []string{pass, p12Pass, p12, notaryKey}

	// A build killed outright (SIGKILL, a crash) never ran its cleanup and left
	// its keychain behind, unlocked; take those away before making a new one.
	sweepStaleKeychains(os.TempDir(), run)
	dir, err := os.MkdirTemp("", fmt.Sprintf("%s%d-*", keychainDirPrefix, os.Getpid()))
	if err != nil {
		return "", noop, err
	}
	kc := filepath.Join(dir, keychainFile)
	created := false
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			if created {
				_, _ = run("security", "delete-keychain", kc)
			}
			_ = os.RemoveAll(dir)
		})
	}
	stopWatch := func() {}
	discard := func() {
		stopWatch()
		cleanup()
	}
	fail := func(step string, err error) (string, func(), error) {
		discard()
		return "", noop, fmt.Errorf("velopack: keychain %s: %s", step, redactValues(err.Error(), secrets...))
	}

	if _, err := run("security", "create-keychain", "-p", pass, kc); err != nil {
		return fail("create", err)
	}
	created = true
	// shiprig installs no signal handling, so a Ctrl-C would end the process
	// before any deferred cleanup ran and leave this keychain unlocked on disk.
	stopWatch = cleanupOnSignal(cleanup)
	// No flags: no lock timeout and no lock on sleep. It is unlocked once, below,
	// and a keychain that locked mid-build (a long compile, a slow notarization)
	// would stop codesign with a prompt nobody is there to answer.
	if _, err := run("security", "set-keychain-settings", kc); err != nil {
		return fail("settings", err)
	}
	if _, err := run("security", "unlock-keychain", "-p", pass, kc); err != nil {
		return fail("unlock", err)
	}

	certFile := filepath.Join(dir, "cert.p12")
	if err := os.WriteFile(certFile, certDER, 0o600); err != nil {
		return fail("write cert", err)
	}
	_, err = run("security", "import", certFile, "-k", kc, "-P", p12Pass, "-T", "/usr/bin/codesign")
	_ = os.Remove(certFile)
	if err != nil {
		return fail("import cert", err)
	}
	// Let codesign use the key without the "allow access" prompt.
	if _, err := run("security", "set-key-partition-list", "-S", "apple-tool:,apple:", "-s", "-k", pass, kc); err != nil {
		return fail("key access", err)
	}
	ids, err := run("security", "find-identity", "-v", "-p", "codesigning", kc)
	if err != nil {
		return fail("list identities", err)
	}
	if !strings.Contains(ids, strings.TrimSpace(m.SignIdentity)) {
		discard()
		return "", noop, fmt.Errorf("velopack: macos.signIdentity %q is not a valid codesigning identity in the cert from %s; it holds:\n%s", m.SignIdentity, envSignP12, strings.TrimSpace(ids))
	}

	if notarize {
		keyFile := filepath.Join(dir, "notary.p8")
		if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
			return fail("write notary key", err)
		}
		args := []string{"notarytool", "store-credentials", strings.TrimSpace(m.NotaryProfile),
			"--key", keyFile, "--key-id", envValue(env, envNotaryKeyID)}
		if iss := strings.TrimSpace(envValue(env, envNotaryIssuer)); iss != "" {
			args = append(args, "--issuer", iss)
		}
		args = append(args, "--keychain", kc)
		_, err := run("xcrun", args...)
		_ = os.Remove(keyFile)
		if err != nil {
			return fail("store notary profile", err)
		}
	}
	return kc, discard, nil
}

const (
	keychainDirPrefix = "shiprig-keychain-" // + the build's pid + "-" + random
	keychainFile      = "signing.keychain-db"
)

// reraise delivers sig to this process again once its handler is gone, so an
// interrupted build still ends the way it would have. A variable for tests.
var reraise = func(sig os.Signal) {
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Signal(sig)
	}
}

// cleanupOnSignal runs cleanup if the process is interrupted or terminated while
// the keychain exists, then re-raises the signal with the default handling back
// in place. The returned stop ends the watch; it is safe to call more than once.
func cleanupOnSignal(cleanup func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			cleanup()
			signal.Stop(ch)
			reraise(sig)
		case <-done:
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			signal.Stop(ch)
			close(done)
		})
	}
}

// sweepStaleKeychains deletes keychains under root left by shiprig builds whose
// process is gone — killed outright, so neither their deferred cleanup nor
// cleanupOnSignal ran. A keychain whose build is still running is left alone.
func sweepStaleKeychains(root string, run keychainRun) {
	dirs, _ := filepath.Glob(filepath.Join(root, keychainDirPrefix+"*"))
	for _, d := range dirs {
		pidText, _, ok := strings.Cut(strings.TrimPrefix(filepath.Base(d), keychainDirPrefix), "-")
		pid, err := strconv.Atoi(pidText)
		if !ok || err != nil || pid == os.Getpid() || processAlive(pid) {
			continue
		}
		if kc := filepath.Join(d, keychainFile); fileExists(kc) {
			_, _ = run("security", "delete-keychain", kc)
		}
		_ = os.RemoveAll(d)
	}
}

// processAlive reports whether pid names a running process. Signal 0 checks
// without delivering anything; EPERM means it exists but isn't ours to signal.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}

// envValue is the value of key in a KEY=VALUE environment; the last entry wins,
// as it does for exec.
func envValue(env []string, key string) string {
	v := ""
	for _, kv := range env {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v = val
		}
	}
	return v
}

// decodeBase64 decodes a base64 secret, ignoring the line breaks and spaces a
// pasted or `base64`-wrapped value carries.
func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
}

// randomPassword is the throwaway keychain's password: never shown, never kept.
func randomPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// redactValues replaces every non-empty secret in s with ***.
func redactValues(s string, secrets ...string) string {
	for _, v := range secrets {
		if v = strings.TrimSpace(v); v != "" {
			s = strings.ReplaceAll(s, v, "***")
		}
	}
	return s
}
