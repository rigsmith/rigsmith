package commitartifact

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/process"
	"golang.org/x/crypto/ssh"
)

func sshFixtureKey(t *testing.T) (ssh.Signer, []byte) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "synthetic fixture")
	if err != nil {
		t.Fatal(err)
	}
	return signer, pem.EncodeToMemory(block)
}

// Real OpenSSH talks to an unprivileged local Go SSH server. The server accepts
// one ephemeral public key and only fixed upload/receive-pack commands; it never
// invokes a shell, reads user SSH state or accesses a real vendor repository.
func sshTransportFixture(t *testing.T, remote gitRepo) (GitTransportOptions, *atomic.Int32) {
	t.Helper()
	executable, err := exec.LookPath("ssh")
	if err != nil {
		t.Fatal("SSH transport tests require OpenSSH:", err)
	}
	identity, private := sshFixtureKey(t)
	host, _ := sshFixtureKey(t)
	server := &ssh.ServerConfig{PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if c.User() == "fixture" && bytes.Equal(key.Marshal(), identity.PublicKey().Marshal()) {
			return nil, nil
		}
		return nil, errors.New("fixture key refused")
	}}
	server.AddHostKey(host)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	var commands atomic.Int32
	wg.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
				defer stop()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
				secured, channels, requests, err := ssh.NewServerConn(conn, server)
				if err != nil {
					return
				}
				defer secured.Close()
				wg.Go(func() { ssh.DiscardRequests(requests) })
				for offered := range channels {
					if offered.ChannelType() != "session" {
						_ = offered.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					channel, requests, err := offered.Accept()
					if err != nil {
						continue
					}
					wg.Go(func() {
						defer channel.Close()
						for request := range requests {
							var payload struct{ Command string }
							if request.Type != "exec" || ssh.Unmarshal(request.Payload, &payload) != nil {
								_ = request.Reply(false, nil)
								continue
							}
							verb := ""
							for _, candidate := range []string{"upload-pack", "receive-pack"} {
								if payload.Command == "git-"+candidate+" '/repo'" || payload.Command == "git-"+candidate+" 'repo'" {
									verb = candidate
								}
							}
							if verb == "" {
								_ = request.Reply(false, nil)
								return
							}
							_ = request.Reply(true, nil)
							commands.Add(1)
							cmd := remote.command(verb, remote.dir)
							// The fixture owns its trusted Git child and closes the
							// channel on cancellation to unblock network stdin.
							input, writer, err := os.Pipe()
							if err != nil {
								return
							}
							defer input.Close()
							wg.Go(func() { defer writer.Close(); _, _ = io.Copy(writer, channel) })
							cmd.Stdin, cmd.Stdout, cmd.Stderr = input, channel, channel.Stderr()
							stop := context.AfterFunc(ctx, func() { _ = channel.Close() })
							runErr := process.Run(ctx, cmd)
							stop()
							code := uint32(0)
							if runErr != nil {
								code = 1
							}
							_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
							return
						}
					})
				}
			})
		}
	})
	t.Cleanup(func() { cancel(); _ = listener.Close(); wg.Wait() })
	// Both shell and SSH-option quoting must preserve spaces and apostrophes.
	dir := filepath.Join(t.TempDir(), "key owner's files~1")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	keyPath, knownPath := filepath.Join(dir, "identity"), filepath.Join(dir, "known hosts")
	if err := os.WriteFile(keyPath, private, 0600); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	known := "[127.0.0.1]:" + port + " " + string(ssh.MarshalAuthorizedKey(host.PublicKey()))
	if err := os.WriteFile(knownPath, []byte(known), 0600); err != nil {
		t.Fatal(err)
	}
	return GitTransportOptions{Remote: "ssh://fixture@127.0.0.1:" + port + "/repo", Branch: "main", SSH: &SSHOptions{Executable: executable, IdentityFile: keyPath, KnownHostsFile: knownPath}}, &commands
}

func TestSSHTransportPublishesAndConfirms(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			req, remote, local, parent := publicationFixture(t, format)
			options, commands := sshTransportFixture(t, remote.repo)
			tr, err := NewGitTransport(options)
			if err != nil {
				t.Fatal(err)
			}
			if destination, branch := tr.Destination(); destination != options.Remote || branch != "main" {
				t.Fatal("destination changed")
			}
			if err := remote.repo.importRef(t.Context(), local.dir, parent, "refs/heads/main", parent); err != nil {
				t.Fatal(err)
			}
			remoteHead := newPublicationCommit(t, remote.repo, parent, "remote.txt", "newer remote")
			mustRun(t, remote.repo, "", "update-ref", "refs/heads/main", remoteHead)
			localHead := newPublicationCommit(t, local, parent, "local.txt", "newer local")
			req.Remote, req.LocalDir, req.LocalCommit = tr, local.dir, localHead
			result, err := Publish(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			for _, sha := range []string{result.CaptureCommit, localHead, remoteHead} {
				if ok, err := remote.repo.ancestor(t.Context(), sha, result.RemoteCommit); err != nil || !ok {
					t.Fatalf("lost ancestry: %v", err)
				}
			}
			again, err := Publish(t.Context(), req)
			if err != nil || again != result || commands.Load() == 0 {
				t.Fatalf("replay: %+v %v", again, err)
			}
		})
	}
}

func TestSSHRemoteAndOptionsValidation(t *testing.T) {
	base := SSHOptions{Executable: filepath.Join(t.TempDir(), "SSH~1"), IdentityFile: filepath.Join(t.TempDir(), "KEY~1"), KnownHostsFile: filepath.Join(t.TempDir(), "HOSTS~1")}
	for _, remote := range []string{"git@example.com:acme/repo.git", "git@example.com:/srv/repo.git", "git@[::1]:repo", "ssh://git@example.com/repo", "ssh://git@[::1]:2222/repo"} {
		if _, err := NewGitTransport(GitTransportOptions{Remote: remote, Branch: "main", SSH: &base}); err != nil {
			t.Fatalf("%s: %v", remote, err)
		}
	}
	for _, remote := range []string{"ssh://git@[example.com]/repo", "git@[example.com]:repo", "ssh://example.com/repo", "ssh://git:password@example.com/repo", "ssh://git@example.com:/repo", "ssh://git@example.com:0/repo", "ssh://git@example.com:65536/repo", "ssh://git@example.com/repo?", "ssh://git@example.com/repo#", "ssh://git@example.com/%72epo", "git@-host:repo", "-git@example.com:repo", "git@example.com:-repo", "git@example.com:~/repo", "git@example.com:repo;touch marker", "git@example.com:repo'", "git@example.com:a/../repo", "git@example.com:", "git@host:repo\\path", "ssh://git@[::1%25en0]/repo"} {
		if _, err := NewGitTransport(GitTransportOptions{Remote: remote, Branch: "main", SSH: &base}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %q: %v", remote, err)
		}
	}
	for _, field := range []string{"executable", "identity", "hosts"} {
		for _, path := range []string{"", "relative", "~/identity", "~other/identity", filepath.Join(t.TempDir(), "${HOME}"), filepath.Join(t.TempDir(), "%h"), filepath.Join(t.TempDir(), "quote\""), filepath.Join(t.TempDir(), "line\n")} {
			t.Run(fmt.Sprintf("%s/%q", field, path), func(t *testing.T) {
				value := base
				switch field {
				case "executable":
					value.Executable = path
				case "identity":
					value.IdentityFile = path
				case "hosts":
					value.KnownHostsFile = path
				}
				if _, err := NewGitTransport(GitTransportOptions{Remote: "git@example.com:repo", Branch: "main", SSH: &value}); !errors.Is(err, ErrInvalid) {
					t.Fatal("unsafe SSH path accepted")
				}
			})
		}
	}
	if _, err := NewGitTransport(GitTransportOptions{Remote: "git@example.com:repo", Branch: "main", SSH: &base, Credential: &HTTPCredential{Username: "x", Password: "y"}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("HTTP credential accepted for SSH")
	}
	if _, err := NewGitTransport(GitTransportOptions{Remote: "git@example.com:repo", Branch: "main", SSH: &base, CAFile: base.KnownHostsFile}); !errors.Is(err, ErrInvalid) {
		t.Fatal("HTTPS CA accepted for SSH")
	}
	if _, err := NewGitTransport(GitTransportOptions{Remote: "https://example.com/repo", Branch: "main", SSH: &base}); !errors.Is(err, ErrInvalid) {
		t.Fatal("SSH identity accepted for HTTPS")
	}
}

func TestSSHTransportTrustAuthenticationAndIsolation(t *testing.T) {
	_, remote, local, parent := publicationFixture(t, "sha1")
	options, commands := sshTransportFixture(t, remote.repo)
	private, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "private"), parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := private.importRef(t.Context(), local.dir, parent, RefName, parent); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(options.SSH.IdentityFile)
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := os.ReadFile(options.SSH.KnownHostsFile)
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(private.dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	putPublicationFile(t, home, ".gitconfig", "[url \"ssh://fixture@invalid.example/\"]\n insteadOf = ssh://fixture@127.0.0.1\n")
	putPublicationFile(t, home, ".ssh/config", "Host *\n ProxyCommand exit 91\n IdentityFile missing\n")
	for name, value := range map[string]string{
		"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": home,
		"GIT_CONFIG_GLOBAL": filepath.Join(home, ".gitconfig"), "GIT_SSH_COMMAND": "exit 92", "GIT_SSH": "missing", "GIT_SSH_VARIANT": "plink",
		"SSH_AUTH_SOCK": filepath.Join(home, "missing-agent"), "SSH_ASKPASS": "missing-askpass", "SSH_ASKPASS_REQUIRE": "force", "SSH_SK_PROVIDER": "missing-provider",
	} {
		t.Setenv(name, value)
	}
	tr, err := NewGitTransport(options)
	if err != nil {
		t.Fatal(err)
	}
	// Editing the caller's option struct cannot replace the selected files.
	originalSSH := *options.SSH
	options.SSH.IdentityFile, options.SSH.KnownHostsFile = "wrong", "wrong"
	mustRun(t, private, "", "update-ref", remoteRefName, parent)
	if sha, err := tr.Fetch(t.Context(), private.dir, remoteRefName); err != nil || sha != "" {
		t.Fatalf("absent: %s %v", sha, err)
	}
	if got := mustRun(t, private, "", "for-each-ref", "--format=%(refname)", remoteRefName); got != "" {
		t.Fatal("stale ref retained")
	}
	if got := mustRun(t, private, "", "rev-parse", RefName); got != parent {
		t.Fatal("capture ref changed")
	}
	for _, tc := range []string{"wrong-key", "missing-key", "encrypted-key", "unknown-host", "changed-host", "missing-hosts", "wrong-user"} {
		t.Run(tc, func(t *testing.T) {
			bad := options
			value := originalSSH
			bad.SSH = &value
			switch tc {
			case "wrong-key":
				_, data := sshFixtureKey(t)
				value.IdentityFile = filepath.Join(t.TempDir(), "wrong")
				if err := os.WriteFile(value.IdentityFile, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-key":
				value.IdentityFile = filepath.Join(t.TempDir(), "missing")
			case "encrypted-key":
				raw, err := ssh.ParseRawPrivateKey(key)
				if err != nil {
					t.Fatal(err)
				}
				block, err := ssh.MarshalPrivateKeyWithPassphrase(raw, "fixture", []byte("synthetic passphrase"))
				if err != nil {
					t.Fatal(err)
				}
				value.IdentityFile = filepath.Join(t.TempDir(), "encrypted")
				if err := os.WriteFile(value.IdentityFile, pem.EncodeToMemory(block), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-hosts":
				value.KnownHostsFile = filepath.Join(t.TempDir(), "missing")
			case "unknown-host", "changed-host":
				value.KnownHostsFile = filepath.Join(t.TempDir(), "untrusted")
				var data []byte
				if tc == "changed-host" {
					other, _ := sshFixtureKey(t)
					data = []byte(strings.Fields(string(hosts))[0] + " " + string(ssh.MarshalAuthorizedKey(other.PublicKey())))
				}
				if err := os.WriteFile(value.KnownHostsFile, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong-user":
				bad.Remote = strings.Replace(options.Remote, "fixture@", "other@", 1)
			}
			tr, err := NewGitTransport(bad)
			if err != nil {
				t.Fatal(err)
			}
			before := commands.Load()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if _, err := tr.Fetch(ctx, private.dir, remoteRefName); !errors.Is(err, ErrTransport) || ctx.Err() != nil {
				t.Fatalf("must fail without prompting/absence: %v", err)
			}
			if commands.Load() != before {
				t.Fatal("Git command crossed rejected authentication or trust")
			}
		})
	}
	for path, before := range map[string][]byte{originalSSH.IdentityFile: key, originalSSH.KnownHostsFile: hosts, filepath.Join(private.dir, "config"): config} {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("transport changed owned file", err)
		}
	}
	if _, err := os.Stat(filepath.Join(private.dir, "FETCH_HEAD")); !os.IsNotExist(err) {
		t.Fatal("FETCH_HEAD created")
	}
}

func TestSSHTransportCancelsBlockedHandshake(t *testing.T) {
	_, remote, _, parent := publicationFixture(t, "sha1")
	options, _ := sshTransportFixture(t, remote.repo)
	private, err := initRepo(t.Context(), filepath.Join(t.TempDir(), "private"), parent)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		cancel()
		var b [4096]byte
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, err := conn.Read(b[:]); err != nil {
				return
			}
		}
	}()
	options.Remote = "ssh://fixture@" + listener.Addr().String() + "/repo"
	tr, err := NewGitTransport(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Fetch(ctx, private.dir, remoteRefName); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_ = listener.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSH connection survived cancellation")
	}
}

func TestSSHMissingIdentityDoesNotEnableDefaults(t *testing.T) {
	executable, err := exec.LookPath("ssh")
	if err != nil {
		t.Fatal(err)
	}
	options := SSHOptions{Executable: executable, IdentityFile: filepath.Join(t.TempDir(), "RUNNER~1", "missing key"), KnownHostsFile: filepath.Join(t.TempDir(), "RUNNER~1", "missing hosts")}
	args, err := sshArguments(options)
	if err != nil {
		t.Fatal(err)
	}
	// -G evaluates OpenSSH's actual defaults without opening a network connection.
	args = append(args, "-G", "git@example.com")
	out, err := exec.CommandContext(t.Context(), args[0], args[1:]...).Output()
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string][]string{}
	for _, line := range strings.Split(string(out), "\n") {
		// Native Windows OpenSSH emits CRLF; preserve the option value itself.
		key, value, ok := strings.Cut(strings.TrimSuffix(line, "\r"), " ")
		if ok {
			settings[key] = append(settings[key], value)
		}
	}
	for key, want := range map[string]string{
		"identityfile":       filepath.ToSlash(options.IdentityFile),
		"userknownhostsfile": filepath.ToSlash(options.KnownHostsFile),
		"identityagent":      "none", "certificatefile": "none", "globalknownhostsfile": "none",
		"batchmode": "yes", "identitiesonly": "yes", "stricthostkeychecking": "true",
		"passwordauthentication": "no", "kbdinteractiveauthentication": "no", "preferredauthentications": "publickey",
		"updatehostkeys": "false", "controlmaster": "false", "forwardagent": "no",
	} {
		got := settings[key]
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s: %q; want only %q", key, got, want)
		}
	}
}
