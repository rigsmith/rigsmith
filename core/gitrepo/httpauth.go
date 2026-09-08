package gitrepo

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
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
//
// Env is the same thing for a caller outside this package, which needs it to
// hand the credential to a child process that runs git itself.
func (a *HTTPAuth) Env() []string { return a.env() }

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

// ghLookup is how the GitHub CLI is asked for a credential. A variable so tests
// can take gh out of the picture: several of them assert what git's own helper
// answered, and on a machine that is logged into gh the real binary would answer
// first and they would be testing nothing.
var ghLookup = ghCredential

// CredentialFor resolves the credential for remoteURL, asking the GitHub CLI
// first and git's own credential helpers second, and returning nil when neither
// has one.
//
// gh goes first because the two can disagree and only one of them is
// maintained. `gh auth login` keeps its token current and scoped; a helper's
// stored entry is whatever was written the last time something authenticated —
// on a machine where that is an older token, it still authenticates, and it
// still cannot read a private repo. josh answers a request it cannot authorise
// with an empty history rather than a refusal, so preferring the stale
// credential does not fail the fetch: it imports nothing and calls it success.
// Asking gh first is what keeps the good token from losing to the old one.
//
// Falling back to `git credential fill` means whatever the user already set up —
// the macOS keychain, Git Credential Manager — still answers when gh is absent
// or knows nothing about the host, and no new secret has to be stored anywhere
// for rig's benefit.
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
	query := credentialQuery(u)

	// Memoized for the life of the process. A stack operation asks for the same
	// host several times — ls-remote to resolve the tip, then the fetch — and
	// each miss is two subprocesses, one of which may unlock a keyring. rig
	// commands are short-lived, so a credential rotated mid-run is not a case
	// worth invalidating for.
	if v, ok := credCache.Load(query); ok {
		return v.(*HTTPAuth).clone(), nil
	}
	a := resolveCredential(ctx, query)
	credCache.Store(query, a)
	// A copy per caller: callers set URLPrefix to scope the header to their own
	// remote, and handing out the cached pointer would let one caller's scope
	// follow the credential into the next one's request.
	return a.clone(), nil
}

// credCache maps a credential-helper query to what answered it, or to a typed
// nil when nothing did — a miss is worth remembering too, since the lookup that
// found nothing costs the same as the one that found something.
var credCache sync.Map

func (a *HTTPAuth) clone() *HTTPAuth {
	if a == nil {
		return nil
	}
	c := *a
	return &c
}

// resetCredentialCache drops what has been resolved so far. For tests, which
// change the configured helper between cases and would otherwise be answered
// with the previous case's credential.
func resetCredentialCache() { credCache = sync.Map{} }

func resolveCredential(ctx context.Context, query string) *HTTPAuth {
	if a := ghLookup(ctx, query); a != nil {
		return a
	}
	out, err := runGitStdin(ctx, "", query, []string{"GIT_TERMINAL_PROMPT=0"}, "credential", "fill")
	if err != nil {
		return nil // no helper, or none of them knows this remote
	}
	return parseCredential(out)
}

// credentialQuery describes the remote to a credential helper the way git would
// describe it. A hand-split host keeps any userinfo attached, and helpers do not
// match "alice@example.com" against credentials stored for "example.com";
// omitting the username and path loses entries scoped to either, which is how
// credential.useHttpPath is configured to work.
func credentialQuery(u *url.URL) string {
	var q strings.Builder
	fmt.Fprintf(&q, "protocol=%s\nhost=%s\n", u.Scheme, u.Host)
	if user := u.User.Username(); user != "" {
		fmt.Fprintf(&q, "username=%s\n", user)
	}
	if p := strings.TrimPrefix(u.Path, "/"); p != "" {
		fmt.Fprintf(&q, "path=%s\n", p)
	}
	q.WriteString("\n")
	return q.String()
}

// ghCredential asks the GitHub CLI for the credential it holds for this host,
// over the same helper protocol `gh auth setup-git` wires into git — so a user
// who never ran that command still gets the token their `gh auth status` says
// they have.
//
// Every failure is the same answer: gh not installed, not logged in, or logged
// in to some other forge. It exits 0 with no output for a host it does not know,
// which parseCredential reports as nothing found.
func ghCredential(ctx context.Context, query string) *HTTPAuth {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil
	}
	// Bounded: this runs on the way to a fetch that has its own deadline, and a
	// gh that hangs on a locked keyring would otherwise hang the pull with it.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "auth", "git-credential", "get")
	cmd.Stdin = strings.NewReader(query)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	return parseCredential(out.String())
}

// parseCredential reads a credential helper's reply. nil when there is no
// password in it, which is how both callers say "nothing found".
func parseCredential(out string) *HTTPAuth {
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
		return nil
	}
	return a
}
