package gitrepo

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// HTTPAuth is an HTTP Basic credential for a single git invocation, scoped to
// one URL prefix.
//
// It travels in the environment, never in argv. `git -c http.extraHeader=…`
// would put the secret in every process listing on the machine, and runGit
// quotes its arguments into the error it returns, so a failed fetch would print
// the token to the terminal and into any log the caller keeps.
type HTTPAuth struct {
	// URLPrefix scopes the header to one remote, so a redirect elsewhere cannot
	// carry the credential with it. Empty attaches it to every HTTP request the
	// invocation makes, which is only safe when the caller controls them all.
	URLPrefix string
	Username  string
	Password  string
}

// env returns the GIT_CONFIG_* pairs that add the Authorization header for this
// invocation, appended after any the caller's environment already carries so a
// user who sets GIT_CONFIG_COUNT for their own reasons does not lose it.
func (a *HTTPAuth) env() []string {
	if a == nil || a.Password == "" {
		return nil
	}
	key := "http.extraHeader"
	if a.URLPrefix != "" {
		key = "http." + a.URLPrefix + ".extraHeader"
	}
	token := base64.StdEncoding.EncodeToString([]byte(a.Username + ":" + a.Password))
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	if n < 0 {
		n = 0
	}
	return []string{
		"GIT_CONFIG_COUNT=" + strconv.Itoa(n+1),
		fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", n, key),
		fmt.Sprintf("GIT_CONFIG_VALUE_%d=Authorization: Basic %s", n, token),
	}
}

// CredentialFor asks git's own credential helpers for the stored credential for
// remoteURL, returning nil when none is configured.
//
// Going through `git credential fill` rather than reading a token from the
// environment means whatever the user already set up — the macOS keychain, the
// GitHub CLI's helper, Git Credential Manager — is what answers, and no new
// secret has to be stored anywhere for rig's benefit.
//
// Terminal prompting is disabled: this is a speculative lookup on a path that
// works anonymously for a public remote, so a missing credential is an answer
// rather than a reason to interrupt someone. A GUI askpass the user configured
// themselves can still surface, exactly as it would for a direct fetch.
func CredentialFor(ctx context.Context, remoteURL string) (*HTTPAuth, error) {
	proto, rest, ok := strings.Cut(remoteURL, "://")
	if !ok || (proto != "https" && proto != "http") {
		return nil, nil // ssh and friends carry their own credentials
	}
	host, _, _ := strings.Cut(rest, "/")
	in := fmt.Sprintf("protocol=%s\nhost=%s\n\n", proto, host)

	out, err := runGitStdin(ctx, "", in, []string{"GIT_TERMINAL_PROMPT=0"}, "credential", "fill")
	if err != nil {
		return nil, nil // no helper, or none of them knows this host
	}
	a := &HTTPAuth{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "username":
			a.Username = v
		case "password":
			a.Password = v
		}
	}
	if a.Password == "" {
		return nil, nil
	}
	return a, nil
}
