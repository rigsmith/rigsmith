package commitartifact

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/artifact"
	"github.com/rigsmith/rigsmith/internal/agentrig/process"
)

// ErrCredentialHelper deliberately omits helper output, argv and credentials.
var ErrCredentialHelper = errors.New("retained Git credential lookup failed")

// CredentialHelperOptions selects a trusted Git credential-protocol executable
// and account. Args contains only non-secret helper options, never shell text.
// HomeDir explicitly selects its credential home; ambient Git configuration and
// helper selection are disabled. Callers must select a helper that honors the
// noninteractive controls below: arbitrary helpers can still display native UI.
// Files, executable and credential-store selection must stay stable during lookup.
type CredentialHelperOptions struct {
	Executable, HomeDir, Username string
	Args                          []string
}

// NewGitTransportWithCredentialHelper resolves one HTTPS credential with the
// helper's get operation, then binds it to the exact destination. No store/erase,
// fallback helper or anonymous fallback is attempted. Construct a new transport
// for each publication attempt to pick up refreshed credentials; this object
// retains only the resulting authorization header and optional expiry in memory.
// This internal seam does not discover helpers or activate commands/queued hooks.
func NewGitTransportWithCredentialHelper(ctx context.Context, options GitTransportOptions, helper CredentialHelperOptions) (*GitTransport, error) {
	if options.Credential != nil {
		return nil, ErrInvalid
	}
	t, err := NewGitTransport(options)
	if err != nil || t.protocol != "https" {
		return nil, ErrInvalid
	}
	if !credentialPath(helper.Executable) || !credentialPath(helper.HomeDir) ||
		helper.Username == "" || len(helper.Username) > 1024 || strings.ContainsAny(helper.Username, ":\x00\r\n") || len(helper.Args) > 16 {
		return nil, ErrInvalid
	}
	size := 0
	for _, arg := range helper.Args {
		size += len(arg)
		if strings.ContainsAny(arg, "\x00\r\n") || size > 8192 {
			return nil, ErrInvalid
		}
	}
	u, _ := url.Parse(options.Remote) // already validated by NewGitTransport
	fields := map[string]string{"protocol": "https", "host": u.Host, "path": strings.TrimPrefix(u.Path, "/"), "username": helper.Username}
	// URL decoding must not turn a remote path into credential-protocol lines.
	for _, value := range fields {
		if strings.ContainsAny(value, "\x00\r\n") {
			return nil, ErrInvalid
		}
	}
	input := "protocol=https\nhost=" + fields["host"] + "\npath=" + fields["path"] + "\nusername=" + helper.Username + "\n\n"
	credential, expiry, err := lookupCredential(ctx, helper, input, fields)
	if err != nil {
		return nil, err
	}
	options.Credential = &credential
	t, err = NewGitTransport(options)
	if err == nil {
		t.credentialExpiry = expiry
	}
	return t, err
}

func credentialPath(path string) bool {
	return filepath.IsAbs(path) && len(path) <= 4096 && !strings.ContainsAny(path, "\x00\r\n")
}

func lookupCredential(ctx context.Context, helper CredentialHelperOptions, input string, fields map[string]string) (HTTPCredential, int64, error) {
	// Lookup has a ceiling even when the caller has no deadline. Owned cleanup is
	// joined before return, including on overflow or a descendant retaining stdout.
	childCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := childCtx.Err(); err != nil {
		return HTTPCredential{}, 0, err
	}
	dir, err := os.MkdirTemp("", "rig-credential-*")
	if err != nil {
		return HTTPCredential{}, 0, ErrCredentialHelper
	}
	defer os.RemoveAll(dir)
	args := append(append([]string(nil), helper.Args...), "get")
	cmd := exec.Command(helper.Executable, args...)
	cmd.Dir = dir
	cmd.Env = credentialEnvironment(helper.HomeDir)
	cmd.Env = append(cmd.Env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir))
	cmd.Stdin = strings.NewReader(input)
	var output bytes.Buffer
	bound := &boundedOutput{w: &output, left: 32 << 10}
	cmd.Stdout = &cancelOutput{writer: bound, cancel: cancel}
	// Stderr is discarded, not captured or included in returned errors.
	err = process.Run(childCtx, cmd)
	if ctx.Err() != nil {
		return HTTPCredential{}, 0, ctx.Err()
	}
	if bound.exceeded {
		return HTTPCredential{}, 0, errors.Join(ErrCredentialHelper, artifact.ErrTooLarge)
	}
	if childCtx.Err() != nil {
		return HTTPCredential{}, 0, childCtx.Err()
	}
	if err != nil {
		return HTTPCredential{}, 0, ErrCredentialHelper
	}
	return parseCredential(output.String(), fields, time.Now().Unix())
}

func credentialEnvironment(home string) []string {
	// Keep only executable/runtime plumbing. In particular no inherited GIT_*,
	// GCM_*, tokens, proxies, askpass, trace, loader or shell startup overrides.
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "PATH", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR":
			env = append(env, entry)
		}
	}
	env = append(env, "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "SSH_ASKPASS_REQUIRE=never",
		"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=credential.interactive", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=credential.helper", "GIT_CONFIG_VALUE_1=",
		"GCM_INTERACTIVE=0", "GCM_GUI_PROMPT=0", "GCM_TRACE=0", "GCM_TRACE_SECRETS=0")
	if runtime.GOOS == "windows" {
		env = append(env, "APPDATA="+filepath.Join(home, "AppData", "Roaming"), "LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"))
	}
	return env
}

func parseCredential(output string, fields map[string]string, now int64) (HTTPCredential, int64, error) {
	fail := func() (HTTPCredential, int64, error) { return HTTPCredential{}, 0, ErrCredentialHelper }
	c := HTTPCredential{Username: fields["username"]}
	var expiry int64
	seen := make(map[string]bool)
	ended := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSuffix(line, "\r") // native Windows helper output
		if line == "" {
			ended = true
			continue
		}
		if ended || strings.ContainsAny(line, "\x00\r") {
			return fail()
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return fail()
		}
		if !strings.HasSuffix(key, "[]") {
			if seen[key] {
				return fail()
			}
			seen[key] = true
		}
		switch key {
		case "protocol", "host", "path", "username":
			if value != fields[key] {
				return fail()
			}
		case "password":
			c.Password = value
		case "password_expiry_utc":
			var err error
			expiry, err = strconv.ParseInt(value, 10, 64)
			if err != nil || expiry <= now {
				return fail()
			}
		case "quit":
			if value != "false" && value != "0" {
				return fail()
			}
		case "url", "authtype", "credential", "continue":
			// No destination rewrite, alternate scheme or multistage handshake.
			return fail()
		default:
			// Git's protocol ignores unknown attributes. This includes helper
			// state and refresh tokens; neither is retained or sent back.
		}
	}
	if c.Password == "" || len(c.Username)+len(c.Password) > 16<<10 {
		return fail()
	}
	return c, expiry, nil
}
