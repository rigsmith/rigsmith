package commitartifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
)

func TestCredentialHelper(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "helper owner's executable")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "testdata/credentialhelper.go")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, out)
	}
	type fixture struct{ Input, Output, Mode, Marker, Report string }
	writeFixture := func(t *testing.T, options GitTransportOptions, mode, output string) (CredentialHelperOptions, fixture, string) {
		t.Helper()
		dir := t.TempDir()
		home := t.TempDir()
		u, err := url.Parse(options.Remote)
		if err != nil {
			t.Fatal(err)
		}
		f := fixture{
			Input:  "protocol=https\nhost=" + u.Host + "\npath=" + strings.TrimPrefix(u.Path, "/") + "\nusername=fixture\n\n",
			Output: output, Mode: mode, Marker: filepath.Join(dir, "child"), Report: filepath.Join(dir, "report.json"),
		}
		data, _ := json.Marshal(f)
		config := filepath.Join(dir, "helper fixture.json")
		if err := os.WriteFile(config, data, 0600); err != nil {
			t.Fatal(err)
		}
		return CredentialHelperOptions{Executable: bin, HomeDir: home, Username: "fixture", Args: []string{config}}, f, config
	}
	const response = "username=fixture\npassword=synthetic transport password\n\n"
	for _, format := range []string{"sha1", "sha256"} {
		t.Run("publication/"+format, func(t *testing.T) {
			req, remote, local, parent := publicationFixture(t, format)
			options, _ := httpTransportFixture(t, remote.repo)
			options.Credential = nil
			helper, _, config := writeFixture(t, options, "", response)
			// get must resolve again for a new attempt, while an existing transport
			// retains its original credential even if the backing store changes.
			tr, err := NewGitTransportWithCredentialHelper(t.Context(), options, helper)
			if err != nil {
				t.Fatal(err)
			}
			if dest, branch := tr.Destination(); dest != options.Remote || branch != "main" {
				t.Fatal("binding changed")
			}
			data, _ := os.ReadFile(config)
			var f fixture
			if err := json.Unmarshal(data, &f); err != nil {
				t.Fatal(err)
			}
			f.Output = "username=fixture\npassword=wrong synthetic password\n\n"
			data, _ = json.Marshal(f)
			if err := os.WriteFile(config, data, 0600); err != nil {
				t.Fatal(err)
			}
			newer, err := NewGitTransportWithCredentialHelper(t.Context(), options, helper)
			if err != nil {
				t.Fatal(err)
			}
			private, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "private"), parent)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = newer.Fetch(t.Context(), private.dir, remoteRefName); !errors.Is(err, ErrTransport) {
				t.Fatal("new attempt reused old credential", err)
			}
			if err := remote.repo.importRef(t.Context(), local.dir, parent, "refs/heads/main", parent); err != nil {
				t.Fatal(err)
			}
			remoteHead := newPublicationCommit(t, remote.repo, parent, "remote.txt", "newer remote")
			mustRun(t, remote.repo, "", "update-ref", "refs/heads/main", remoteHead)
			localHead := newPublicationCommit(t, local, parent, "local.txt", "newer local")
			req.LocalDir, req.LocalCommit, req.Remote = local.dir, localHead, tr
			result, err := Publish(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			for _, sha := range []string{result.CaptureCommit, localHead, remoteHead} {
				if ok, err := remote.repo.ancestor(t.Context(), sha, result.RemoteCommit); err != nil || !ok {
					t.Fatal("lost history", err)
				}
			}
			again, err := Publish(t.Context(), req)
			if err != nil || again != result {
				t.Fatal("replay changed publication", err)
			}
			for _, value := range []any{tr, err} {
				if strings.Contains(fmt.Sprint(value), "synthetic transport password") {
					t.Fatal("credential leaked in formatting")
				}
			}
		})
	}
	t.Run("isolation-and-expiry", func(t *testing.T) {
		_, remote, _, parent := publicationFixture(t, "sha1")
		options, requests := httpTransportFixture(t, remote.repo)
		options.Credential = nil
		expires := time.Now().Unix() + 3600
		helper, f, _ := writeFixture(t, options, "", fmt.Sprintf("password=synthetic transport password\npassword_expiry_utc=%d\n", expires))
		poison := map[string]string{
			"HOME": t.TempDir(), "USERPROFILE": t.TempDir(), "GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "credential.helper", "GIT_CONFIG_VALUE_0": "!unexpected-command",
			"GIT_DIR": t.TempDir(), "GIT_ASKPASS": "unexpected-command", "GIT_TRACE": "unexpected-log", "GCM_TRACE": "unexpected-log", "GCM_TRACE_SECRETS": "1",
			"GCM_INTERACTIVE": "1", "HTTPS_PROXY": "http://127.0.0.1:1", "GH_TOKEN": "synthetic ambient token", "SSH_AUTH_SOCK": "unexpected-agent",
		}
		for key, value := range poison {
			t.Setenv(key, value)
		}
		putPublicationFile(t, helper.HomeDir, ".gitconfig", "[credential]\n helper = !unexpected-command\n")
		tr, err := NewGitTransportWithCredentialHelper(t.Context(), options, helper)
		if err != nil {
			t.Fatal(err)
		}
		if tr.credentialExpiry != expires {
			t.Fatal("helper expiry was not retained")
		}
		data, err := os.ReadFile(f.Report)
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			Dir       string
			Env, Args []string
		}
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{}
		for _, entry := range report.Env {
			key, value, _ := strings.Cut(entry, "=")
			env[strings.ToUpper(key)] = value
		}
		for _, key := range []string{"GIT_DIR", "GIT_TRACE", "HTTPS_PROXY", "GH_TOKEN", "SSH_AUTH_SOCK"} {
			if _, ok := env[key]; ok {
				t.Fatal("inherited environment", key)
			}
		}
		for key, want := range map[string]string{"HOME": helper.HomeDir, "USERPROFILE": helper.HomeDir, "GCM_INTERACTIVE": "0", "GCM_GUI_PROMPT": "0", "GCM_TRACE_SECRETS": "0", "GIT_TERMINAL_PROMPT": "0", "GIT_CONFIG_GLOBAL": os.DevNull} {
			if env[key] != want {
				t.Fatal("wrong environment", key)
			}
		}
		if len(report.Args) != 3 || report.Args[2] != "get" {
			t.Fatal("unexpected helper operation")
		}
		if _, err := os.Stat(report.Dir); !os.IsNotExist(err) {
			t.Fatal("lookup directory retained", err)
		}
		if requests.Load() != 0 {
			t.Fatal("constructor contacted Git remote")
		}
		private, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "private"), parent)
		if err != nil {
			t.Fatal(err)
		}
		tr.credentialExpiry = time.Now().Unix() - 1
		if _, err := tr.Fetch(t.Context(), private.dir, remoteRefName); !errors.Is(err, ErrCredentialHelper) {
			t.Fatal("expired credential used", err)
		}
		if requests.Load() != 0 {
			t.Fatal("expired credential sent")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := tr.Fetch(ctx, private.dir, remoteRefName); !errors.Is(err, context.Canceled) {
			t.Fatal("expiry masked cancellation", err)
		}
	})
	t.Run("refused-response", func(t *testing.T) {
		options := GitTransportOptions{Remote: "https://example.com/repo.git", Branch: "main"}
		for _, output := range []string{"", "password=synthetic\nusername=other\n", "password=synthetic\nhost=other.example\n", "password=synthetic\npassword_expiry_utc=1\n"} {
			helper, _, _ := writeFixture(t, options, "", output)
			tr, err := NewGitTransportWithCredentialHelper(t.Context(), options, helper)
			if tr != nil || !errors.Is(err, ErrCredentialHelper) {
				t.Fatal("refused response produced transport", err)
			}
		}
	})
	for _, mode := range []string{"return", "wait", "overflow", "exit"} {
		t.Run("cleanup/"+mode, func(t *testing.T) {
			options := GitTransportOptions{Remote: "https://example.com:8443/acme/repo.git", Branch: "main"}
			helper, f, _ := writeFixture(t, options, mode, response)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if mode == "wait" {
				go func() {
					if waitCleanupMarker(ctx, f.Marker) {
						cancel()
					}
				}()
			}
			start := time.Now()
			tr, err := NewGitTransportWithCredentialHelper(ctx, options, helper)
			switch mode {
			case "return":
				if err != nil || tr == nil {
					t.Fatal(err)
				}
			case "wait":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "overflow":
				if !errors.Is(err, artifact.ErrTooLarge) || !errors.Is(err, ErrCredentialHelper) {
					t.Fatal(err)
				}
			case "exit":
				if !errors.Is(err, ErrCredentialHelper) {
					t.Fatal(err)
				}
			}
			if err != nil && (tr != nil || strings.Contains(err.Error(), "synthetic")) {
				t.Fatal("partial or sensitive failure")
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("helper cleanup blocked")
			}
			if mode != "exit" {
				if _, err := os.Stat(f.Marker); err != nil {
					t.Fatal("descendant did not start", err)
				}
				if err := os.WriteFile(f.Marker+".released", nil, 0600); err != nil {
					t.Fatal(err)
				}
				time.Sleep(time.Second)
				if _, err := os.Stat(f.Marker + ".late"); !os.IsNotExist(err) {
					t.Fatal("descendant executed after return", err)
				}
			}
		})
	}
	t.Run("invalid-startup-and-cancel", func(t *testing.T) {
		options := GitTransportOptions{Remote: "https://example.com/repo.git", Branch: "main"}
		helper, f, _ := writeFixture(t, options, "", response)
		for _, remote := range []string{"https://example.com/a%0Ab", "https://example.com/a%00b", "http://127.0.0.1/repo", t.TempDir(), "git@example.com:repo", "https://other:secret@example.com/repo"} {
			bad := options
			bad.Remote = remote
			if tr, err := NewGitTransportWithCredentialHelper(t.Context(), bad, helper); tr != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("accepted remote", remote, err)
			}
		}
		for _, change := range []func(*CredentialHelperOptions){
			func(h *CredentialHelperOptions) { h.Executable = "helper" }, func(h *CredentialHelperOptions) { h.HomeDir = "relative" },
			func(h *CredentialHelperOptions) { h.Username = "" }, func(h *CredentialHelperOptions) { h.Username = "other\npassword=injected" },
			func(h *CredentialHelperOptions) { h.Args = []string{"bad\x00argument"} }, func(h *CredentialHelperOptions) { h.Args = make([]string, 17) },
		} {
			bad := helper
			change(&bad)
			if tr, err := NewGitTransportWithCredentialHelper(t.Context(), options, bad); tr != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal("accepted helper options", err)
			}
		}
		bad := options
		bad.Credential = &HTTPCredential{Username: "fixture", Password: "synthetic duplicate"}
		if _, err := NewGitTransportWithCredentialHelper(t.Context(), bad, helper); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted ambiguous credentials", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if tr, err := NewGitTransportWithCredentialHelper(ctx, options, helper); tr != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("pre-cancel", err)
		}
		if _, err := os.Stat(f.Report); !os.IsNotExist(err) {
			t.Fatal("invalid lookup started helper")
		}
		helper.Executable = filepath.Join(t.TempDir(), "missing-helper")
		if tr, err := NewGitTransportWithCredentialHelper(t.Context(), options, helper); tr != nil || !errors.Is(err, ErrCredentialHelper) {
			t.Fatal("startup failure", err)
		}
	})
}

func TestCredentialProtocol(t *testing.T) {
	fields := map[string]string{"protocol": "https", "host": "example.com:8443", "path": "acme/repo.git", "username": "fixture"}
	for _, response := range []string{
		"password=synthetic=secret", "username=fixture\r\npassword=synthetic=secret\r\n\r\n",
		"protocol=https\nhost=example.com:8443\npath=acme/repo.git\npassword=synthetic=secret\n",
		"password=synthetic=secret\npassword_expiry_utc=1001\noauth_refresh_token=synthetic refresh\ncapability[]=state\nstate[]=opaque\nstate[]=second\n",
	} {
		c, expiry, err := parseCredential(response, fields, 1000)
		if err != nil || c.Username != "fixture" || c.Password != "synthetic=secret" || (expiry != 0 && expiry != 1001) {
			t.Fatal("valid helper response refused", err)
		}
	}
	for _, response := range []string{
		"", "username=fixture\n", "password=\n", "password=one\npassword=two\n", "password=one\n\npassword=two\n",
		"password=one\nusername=other\n", "password=one\nhost=other.example\n", "password=one\nprotocol=http\n", "password=one\npath=other/repo\n",
		"password=one\npassword_expiry_utc=1000\n", "password=one\npassword_expiry_utc=invalid\n", "password=one\npassword_expiry_utc=-1\n",
		"password=one\nquit=true\n", "password=one\nquit=1\n", "password=one\ncontinue=true\n", "password=one\nauthtype=bearer\n",
		"password=one\ncredential=opaque\n", "password=one\nurl=https://example.com:8443/acme/repo.git\n",
		"password=one\x00two\n", "password=one\rtwo\n", "password=one\nmalformed\n", "password=one\n=empty-key\n",
		"password=" + strings.Repeat("x", 16<<10),
	} {
		c, expiry, err := parseCredential(response, fields, 1000)
		if !errors.Is(err, ErrCredentialHelper) || c != (HTTPCredential{}) || expiry != 0 {
			t.Fatal("invalid response accepted or partially returned")
		}
	}
}
