package gitrepo

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestHTTPAuthEnv(t *testing.T) {
	t.Run("nothing to send", func(t *testing.T) {
		var nilAuth *HTTPAuth
		if got := nilAuth.env(); got != nil {
			t.Fatalf("nil auth: got %v, want nil", got)
		}
		if got := (&HTTPAuth{Username: "x"}).env(); got != nil {
			t.Fatalf("no password: got %v, want nil", got)
		}
	})

	t.Run("scoped header, credential encoded", func(t *testing.T) {
		t.Setenv("GIT_CONFIG_COUNT", "")
		got := (&HTTPAuth{URLPrefix: "http://127.0.0.1:9/", Username: "u", Password: "tok"}).env()
		want := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("u:tok"))
		for _, expect := range []string{
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.http://127.0.0.1:9/.extraHeader",
			"GIT_CONFIG_VALUE_0=" + want,
		} {
			if !slices.Contains(got, expect) {
				t.Errorf("missing %q in %v", expect, got)
			}
		}
	})

	t.Run("appends to config the caller already set", func(t *testing.T) {
		t.Setenv("GIT_CONFIG_COUNT", "2")
		got := (&HTTPAuth{Password: "tok"}).env()
		if !slices.Contains(got, "GIT_CONFIG_COUNT=3") || !slices.Contains(got, "GIT_CONFIG_KEY_2=http.extraHeader") {
			t.Fatalf("did not append at index 2: %v", got)
		}
	})

	t.Run("the secret never reaches argv", func(t *testing.T) {
		// The whole point of env over `git -c`: runGitStdin quotes its args into
		// the error it returns, so a token in args would be printed on failure.
		for _, kv := range (&HTTPAuth{Password: "s3cret"}).env() {
			if strings.HasPrefix(kv, "GIT_CONFIG_VALUE_") && !strings.Contains(kv, base64.StdEncoding.EncodeToString([]byte(":s3cret"))) {
				t.Fatalf("credential not encoded into the header: %q", kv)
			}
		}
	})
}

func TestCredentialFor(t *testing.T) {
	ctx := context.Background()

	t.Run("non-http remotes carry their own credentials", func(t *testing.T) {
		for _, url := range []string{"git@github.com:acme/pty-core.git", "ssh://git@example.com/x.git", "/local/path"} {
			got, err := CredentialFor(ctx, url)
			if err != nil || got != nil {
				t.Errorf("%s: got (%v, %v), want (nil, nil)", url, got, err)
			}
		}
	})

	t.Run("the remote is described to git the way git would", func(t *testing.T) {
		// The helper records what it was asked; the query is what is under test.
		// Userinfo must not end up in the host, and the username and path have to
		// be passed or entries scoped to either cannot match.
		dir := t.TempDir()
		asked := filepath.Join(dir, "asked.txt")
		cfg := filepath.Join(dir, "gitconfig")
		// Forward slashes: this path goes inside a git-config value, where a
		// backslash is an escape, and then into a shell. Windows temp paths are
		// full of them.
		script := "!f() { cat > " + filepath.ToSlash(asked) + "; echo password=tok; }; f"
		// git forwards the path to a helper only when asked to; without this the
		// value we send is dropped before the helper sees it.
		conf := "[credential]\n\thelper = \"" + script + "\"\n\tuseHttpPath = true\n"
		if err := os.WriteFile(cfg, []byte(conf), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", cfg)
		t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

		if _, err := CredentialFor(ctx, "https://alice@example.com:8443/acme/pty-core.git"); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(asked)
		if err != nil {
			t.Fatalf("the helper was never asked: %v", err)
		}
		for _, want := range []string{
			"protocol=https",
			"host=example.com:8443",
			"username=alice",
			"path=acme/pty-core.git",
		} {
			if !strings.Contains(string(got), want) {
				t.Errorf("missing %q in what git was asked:\n%s", want, got)
			}
		}
		if strings.Contains(string(got), "host=alice@") {
			t.Errorf("userinfo leaked into the host:\n%s", got)
		}
	})

	t.Run("whitespace inside a credential survives", func(t *testing.T) {
		// A password whose own spaces matter. Trimming the whole line eats them
		// and turns a working credential into one that fails to authenticate.
		cfg := filepath.Join(t.TempDir(), "gitconfig")
		script := `!f() { test $1 = get && echo username=u && echo 'password= sp ace '; }; f`
		if err := os.WriteFile(cfg, []byte("[credential]\n\thelper = \""+script+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", cfg)
		t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

		got, err := CredentialFor(ctx, "https://github.com/acme/pty-core.git")
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatal("no credential resolved")
		}
		if got.Password != " sp ace " {
			t.Fatalf("password lost its spaces: %q", got.Password)
		}
	})

	t.Run("reads whatever helper the user configured", func(t *testing.T) {
		cfg := filepath.Join(t.TempDir(), "gitconfig")
		// The value must be quoted: git's config parser treats an unquoted one
		// with shell syntax in it as a parse error, and the helper silently
		// never runs.
		script := `!f() { test $1 = get && echo username=x-access-token && echo password=tok; }; f`
		if err := os.WriteFile(cfg, []byte("[credential]\n\thelper = \""+script+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", cfg)
		t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

		got, err := CredentialFor(ctx, "https://github.com/acme/pty-core.git")
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Fatal("no credential resolved; the configured helper did not run")
		}
		if got.Password != "tok" || got.Username != "x-access-token" {
			t.Fatalf("got username=%q with a password of %d bytes, want the helper's credential", got.Username, len(got.Password))
		}
	})

	t.Run("no helper is an answer, not a failure", func(t *testing.T) {
		cfg := filepath.Join(t.TempDir(), "gitconfig")
		if err := os.WriteFile(cfg, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", cfg)
		t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

		got, err := CredentialFor(ctx, "https://github.com/acme/pty-core.git")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatal("resolved a credential with no helper configured; the test is reaching a real keychain")
		}
	})
}
