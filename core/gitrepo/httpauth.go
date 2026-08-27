package gitrepo

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
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
// rather than a reason to interrupt someone. An askpass program the user
// configured themselves can still surface, exactly as it would for a direct
// fetch of the same host — which is why the documentation promises only that
// rig asks for nothing of its own.
func CredentialFor(ctx context.Context, remoteURL string) (*HTTPAuth, error) {
	u, err := url.Parse(remoteURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, nil // ssh and friends carry their own credentials
	}
	// Describe the remote to git the way git would describe it to itself. A
	// hand-split host keeps any userinfo attached, and helpers do not match
	// "alice@example.com" against credentials stored for "example.com"; omitting
	// the username and path loses entries scoped to either, which is how
	// credential.useHttpPath is configured to work.
	var q strings.Builder
	fmt.Fprintf(&q, "protocol=%s\nhost=%s\n", u.Scheme, u.Host)
	if user := u.User.Username(); user != "" {
		fmt.Fprintf(&q, "username=%s\n", user)
	}
	if p := strings.TrimPrefix(u.Path, "/"); p != "" {
		fmt.Fprintf(&q, "path=%s\n", p)
	}
	q.WriteString("\n")

	out, err := runGitStdin(ctx, "", q.String(), []string{"GIT_TERMINAL_PROMPT=0"}, "credential", "fill")
	if err != nil {
		return nil, nil // no helper, or none of them knows this remote
	}
	a := &HTTPAuth{}
	for _, line := range strings.Split(out, "\n") {
		// Only the line ending is noise. A value's own leading or trailing
		// spaces are part of the credential, and trimming them turns a working
		// password into one that fails authentication.
		k, v, ok := strings.Cut(strings.TrimSuffix(line, "\r"), "=")
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
