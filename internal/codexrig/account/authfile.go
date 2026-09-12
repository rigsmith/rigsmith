// Package account manages several Codex logins on one machine: a store of
// credentials keyed by the account's own email, an isolated CODEX_HOME per
// account so two logins can run side by side, and the machine-wide swap that
// changes which login `codex` uses by default.
//
// It is deliberately simpler than clauderig's equivalent, because Codex is
// simpler in the one way that matters. Claude Code splits identity in two — a
// credential in the Keychain and a display block in ~/.claude.json — which can
// disagree, and most of clauderig's account code exists to detect and repair
// that. Codex keeps one file, auth.json, and the identity is INSIDE it: the
// id_token is a JWT whose claims carry the email, the plan and the account id.
// There is no second place for it to disagree with, so there is no desync model
// here, and anyone porting one in from clauderig is solving a problem Codex does
// not have.
//
// What Codex does have that Claude does not: the credential is a plain file,
// mode 0600, with no OS credential store behind it (`codex features list`
// reports secret_auth_storage as false in 0.144.6). So the file IS the secret.
// Nothing here prints it, logs it, or copies it anywhere but an account's own
// directory.
package account

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rigsmith/rigsmith/internal/codexrig/codexhome"
)

// ErrNoLive is returned when this machine has no Codex credential at all.
var ErrNoLive = errors.New("no Codex credential found (run `codex login` first)")

// AuthModeChatGPT and AuthModeAPIKey are the two values Codex writes into
// auth.json's auth_mode. An API-key login has no id_token and therefore no
// email, which is why CaptureLive has a second naming path for it.
const (
	AuthModeChatGPT = "chatgpt"
	AuthModeAPIKey  = "apikey"
)

// auth mirrors the parts of Codex's auth.json that codexrig reads. Every field
// is optional on purpose: this is another program's file, and a Codex upgrade
// that adds a key must not make the file unreadable. Unknown keys survive a
// round trip because codexrig copies the bytes rather than re-marshalling this
// struct — see storeAuth.
type auth struct {
	AuthMode string `json:"auth_mode"`
	APIKey   string `json:"OPENAI_API_KEY"`
	Tokens   *struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
	LastRefresh string `json:"last_refresh"`
}

// Identity is who a credential authenticates as, read from the credential
// itself. Every field may be empty: an API-key login has no email or plan, and
// an id_token from a future Codex may drop a claim.
type Identity struct {
	Email     string `json:"email,omitempty"`
	Name      string `json:"name,omitempty"`
	AccountID string `json:"accountId,omitempty"`
	PlanType  string `json:"planType,omitempty"`
	AuthMode  string `json:"authMode,omitempty"`
}

// ReadLive reads the machine's live Codex credential: $HOME/.codex/auth.json.
//
// It resolves the home itself rather than taking one, and deliberately ignores
// CODEX_HOME. The live credential is a property of the machine; reading whatever
// one shell happened to export is how an isolated account's token gets filed
// under the machine's identity. Callers that must act on the machine while
// CODEX_HOME points elsewhere check with codexhome.Env and refuse.
func ReadLive() ([]byte, error) {
	home, err := codexhome.Default()
	if err != nil {
		return nil, err
	}
	return readAuthAt(home)
}

// WriteLive replaces the machine's live credential, atomically and 0600.
func WriteLive(raw []byte) error {
	home, err := codexhome.Default()
	if err != nil {
		return err
	}
	return writeAuthAt(home, raw)
}

func readAuthAt(home string) ([]byte, error) {
	b, err := os.ReadFile(codexhome.Auth(home))
	switch {
	case os.IsNotExist(err):
		return nil, ErrNoLive
	case err != nil:
		return nil, err
	}
	return b, nil
}

func writeAuthAt(home string, raw []byte) error {
	if len(raw) == 0 {
		return errors.New("refusing to write an empty credential")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	return atomicWrite(codexhome.Auth(home), raw, 0o600)
}

// HasTokens reports whether a credential blob can actually authenticate — a
// ChatGPT login with a refresh or access token, or an API key.
//
// This is the single check between "a blob we can hand to Codex" and "a file
// that will silently log the machine out". Both directions of every credential
// round trip go through it.
func HasTokens(raw []byte) bool {
	a, err := parseAuth(raw)
	if err != nil {
		return false
	}
	if strings.TrimSpace(a.APIKey) != "" {
		return true
	}
	return a.Tokens != nil &&
		(strings.TrimSpace(a.Tokens.RefreshToken) != "" || strings.TrimSpace(a.Tokens.AccessToken) != "")
}

// IdentityOf reads who a credential authenticates as. It never fails on a
// credential it cannot fully understand: a blob with no id_token yields an
// identity carrying only the auth mode, which is the honest answer.
func IdentityOf(raw []byte) Identity {
	a, err := parseAuth(raw)
	if err != nil {
		return Identity{}
	}
	id := Identity{AuthMode: strings.TrimSpace(a.AuthMode)}
	if id.AuthMode == "" && strings.TrimSpace(a.APIKey) != "" {
		id.AuthMode = AuthModeAPIKey
	}
	if a.Tokens == nil {
		return id
	}
	id.AccountID = strings.TrimSpace(a.Tokens.AccountID)
	c, err := claimsOf(a.Tokens.IDToken)
	if err != nil {
		return id
	}
	id.Email = strings.TrimSpace(c.Email)
	id.Name = strings.TrimSpace(c.Name)
	if c.OpenAI != nil {
		id.PlanType = strings.TrimSpace(c.OpenAI.PlanType)
		if id.AccountID == "" {
			id.AccountID = strings.TrimSpace(c.OpenAI.AccountID)
		}
	}
	return id
}

// idClaims are the id_token claims codexrig reads. The namespaced key is
// OpenAI's own; it is a URL rather than a name, so it cannot be mistaken for a
// standard claim.
type idClaims struct {
	Email  string `json:"email"`
	Name   string `json:"name"`
	OpenAI *struct {
		AccountID string `json:"chatgpt_account_id"`
		PlanType  string `json:"chatgpt_plan_type"`
		UserID    string `json:"chatgpt_user_id"`
	} `json:"https://api.openai.com/auth"`
}

// claimsOf decodes a JWT's payload WITHOUT verifying its signature, and that is
// correct here rather than lazy. codexrig is not deciding whether to trust the
// token — the server does that on every request. It is reading the label the
// user already agreed to, to file the credential under the right name. Verifying
// would mean fetching and pinning OpenAI's signing keys to answer a question
// nobody asked; an expired token still names its owner, and refusing to read one
// would leave a stored account permanently unidentifiable.
func claimsOf(jwt string) (idClaims, error) {
	var c idClaims
	parts := strings.Split(strings.TrimSpace(jwt), ".")
	if len(parts) < 2 {
		return c, errors.New("not a JWT")
	}
	// base64url, and the segments are unpadded.
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Tolerate a padded encoding rather than losing the identity over it.
		body, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return c, err
		}
	}
	if err := json.Unmarshal(body, &c); err != nil {
		return c, err
	}
	return c, nil
}

func parseAuth(raw []byte) (auth, error) {
	var a auth
	if len(raw) == 0 {
		return a, errors.New("empty credential")
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return a, fmt.Errorf("credential is not JSON: %w", err)
	}
	return a, nil
}

// atomicWrite writes data to path via a temp sibling and a rename, so a reader
// (Codex itself, mid-refresh) never sees a half-written credential. It follows a
// symlink at the destination: someone who pointed auth.json at a managed
// location meant the content to land there, not for the link to be replaced.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}
