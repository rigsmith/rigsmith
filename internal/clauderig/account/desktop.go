package account

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Claude Desktop session capture/restore.
//
// Switching accounts in Desktop normally costs a login, because Desktop keeps
// tokens for the ACTIVE account only: signing into B discards A's. Nothing is
// wrong with A's session at that point — it has simply been thrown away. So the
// whole feature is to snapshot it first and put it back later.
//
// Desktop authenticates on two independent surfaces and BOTH have to move
// together, or the app comes up half-switched — API calls as one account, web UI
// as the other:
//
//   - the OAuth token cache, in config.json under `oauth:tokenCache` and
//     `oauth:tokenCacheV2` (plus `lastKnownAccountUuid`, which selects the account)
//   - the web session, in the Cookies SQLite DB — `sessionKey` and friends on
//     claude.ai, which is what the Electron webview authenticates with
//
// Both are Electron safeStorage ciphertext ("v10" blobs) sealed with a key that
// belongs to THIS machine's keychain. That is what makes this design safe: the
// snapshot is stored as ciphertext, copied verbatim, and never decrypted, so no
// plaintext OAuth material is ever written to disk. It is also what makes the
// snapshot strictly machine-local — the same bytes are meaningless elsewhere,
// which is why Apply refuses a snapshot from another host rather than writing
// undecryptable garbage and producing a mystery logout.
//
// Verified end to end on macOS 2026-08-18: clearing both artifacts logs Desktop
// out (it sets windowSizeWasSignedIn=false and does not self-heal); writing them
// back verbatim brings the session back, with the blobs byte-identical
// afterwards — Desktop accepts them as-is rather than re-authenticating.
//
// Desktop must be QUIT for any of this: it holds the Cookies DB open and
// rewrites config.json on exit, so a write underneath a running app is clobbered.

// desktopOAuthKeys are the config.json keys that carry the Desktop session.
// `lastKnownAccountUuid` is included because it selects which account the app
// opens as; without it the tokens would be right and the UI would still be
// pointed at the previous account.
var desktopOAuthKeys = []string{
	"oauth:tokenCache",
	"oauth:tokenCacheV2",
	"lastKnownAccountUuid",
}

// desktopCookieHosts scopes the cookie swap. Only claude.ai carries the session;
// everything else in the shared Cookies DB (other sites, analytics, Cloudflare
// clearances for unrelated hosts) belongs to the machine, not to the account, and
// is left strictly alone.
// Matched as the apex host or a dot-delimited subdomain, NOT a bare suffix: a
// plain `LIKE '%claude.ai'` also matches `notclaude.ai` and would export and
// delete an unrelated site's cookies.
const desktopCookieHosts = `(host_key = 'claude.ai' OR host_key LIKE '%.claude.ai')`

// desktopRunning is a package var so tests are not at the mercy of whether the
// developer happens to have Desktop open — an assertion that passes or fails
// depending on the state of an unrelated app is worse than no assertion.
var desktopRunning = DesktopRunning

// ErrDesktopUnsupported means Desktop switching is not implemented for this OS.
// Returned rather than silently doing nothing: a feature that quietly records no
// session looks identical to one that has nothing to record.
var ErrDesktopUnsupported = errors.New("Claude Desktop account switching is only supported on macOS")

// DesktopSupported reports whether this build can capture and restore Desktop
// sessions.
func DesktopSupported() bool { return desktopSupported }

// ErrNoDesktop means this machine has no Claude Desktop data directory.
var ErrNoDesktop = errors.New("no Claude Desktop data directory on this machine")

// ErrDesktopRunning means Desktop is open and would clobber the write.
var ErrDesktopRunning = errors.New("Claude Desktop is running")

// DesktopSnapshot is one account's Desktop session, captured verbatim. Every
// value in it is ciphertext or an opaque identifier; nothing here is readable
// without this machine's keychain.
type DesktopSnapshot struct {
	// Host and OS bind the snapshot to the machine that made it. The ciphertext
	// is sealed with a machine-local key, so applying it anywhere else yields an
	// unreadable session rather than an error at write time.
	Host string `json:"host"`
	OS   string `json:"os"`
	// ConfigKeys holds the desktopOAuthKeys that were present, as raw JSON so the
	// values round-trip byte-exactly.
	ConfigKeys map[string]json.RawMessage `json:"configKeys"`
	// CookieSQL is a list of INSERT statements naming their columns explicitly.
	// Storing SQL rather than parsed rows keeps blobs binary-safe (X'..' literals)
	// without inventing an encoding, and naming the columns is what survives
	// Chromium changing this table between Desktop versions.
	CookieSQL  []string  `json:"cookieSql"`
	CapturedAt time.Time `json:"capturedAt"`
}

// HasSession reports whether the snapshot actually carries a login. An empty
// snapshot is a valid thing to store (an account captured while Desktop was
// logged out) but must never be applied over a working session.
func (d *DesktopSnapshot) HasSession() bool {
	if d == nil {
		return false
	}
	_, v1 := d.ConfigKeys["oauth:tokenCache"]
	_, v2 := d.ConfigKeys["oauth:tokenCacheV2"]
	return v1 || v2
}

// AccountUUID is the account Desktop was signed in as when this was captured,
// taken from `lastKnownAccountUuid` — the one piece of Desktop's identity that
// is plaintext (the org uuid is sealed inside the token cache). It is what lets
// a snapshot be matched to a CLI account, since the CLI's own oauthAccount block
// carries the same uuid. "" when Desktop has never recorded one.
func (d *DesktopSnapshot) AccountUUID() string {
	if d == nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(d.ConfigKeys["lastKnownAccountUuid"], &s); err != nil {
		return ""
	}
	return s
}

// ProfileAccountUUID is the account uuid from a stored oauthAccount block, the
// CLI-side half of the comparison above. Mirrors ProfileOrg.
func ProfileAccountUUID(raw []byte) string { return parseOAuthMeta(raw).AccountUUID }

// DesktopMatch is whether a Desktop snapshot belongs to a given CLI account.
type DesktopMatch int

const (
	// DesktopUnknown — one side has no account uuid, so nothing can be proven.
	// Callers must treat this as "not a match": filing a session under the wrong
	// account is worse than not filing it at all.
	DesktopUnknown DesktopMatch = iota
	DesktopSame
	DesktopDifferent
)

// MatchDesktopAccount compares a Desktop snapshot against a CLI account's
// oauthAccount block. The CLI and Desktop are separate logins that routinely
// disagree — signed in as different accounts at the same time — so this is what
// stops Desktop-account-B's session being stored under CLI-account-A and then
// restored by a later switch to A.
func MatchDesktopAccount(snap *DesktopSnapshot, oauthBlock []byte) DesktopMatch {
	got, want := snap.AccountUUID(), ProfileAccountUUID(oauthBlock)
	if got == "" || want == "" {
		return DesktopUnknown
	}
	if got == want {
		return DesktopSame
	}
	return DesktopDifferent
}

// DesktopHome returns this machine's Claude Desktop data directory, mirroring
// config.DefaultRoots' desktop locations. Kept here as a plain per-OS lookup —
// the same shape as ClaudeHome — so the account package doesn't need a Config
// and a Machine threaded through it just to find one directory.
func DesktopHome() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(h, "Library", "Application Support", "Claude"), nil
	case "windows":
		return filepath.Join(h, "AppData", "Roaming", "Claude"), nil
	default:
		return filepath.Join(h, ".config", "Claude"), nil
	}
}

func desktopConfigPath(root string) string  { return filepath.Join(root, "config.json") }
func desktopCookiesPath(root string) string { return filepath.Join(root, "Cookies") }

// CaptureDesktop snapshots the live Desktop session. It reads only; Desktop may
// be running, though a snapshot taken while it is running can be superseded
// moments later by a token refresh — which is why `switch` captures the outgoing
// account after confirming Desktop is closed.
func CaptureDesktop(root string) (*DesktopSnapshot, error) {
	if !desktopSupported {
		return nil, ErrDesktopUnsupported
	}
	if !dirExists(root) {
		return nil, ErrNoDesktop
	}
	host, _ := os.Hostname()
	snap := &DesktopSnapshot{
		Host:       host,
		OS:         runtime.GOOS,
		ConfigKeys: map[string]json.RawMessage{},
		CapturedAt: time.Now().UTC(),
	}
	doc, err := readDesktopConfig(root)
	if err != nil {
		return nil, err
	}
	for _, k := range desktopOAuthKeys {
		if v, ok := doc[k]; ok {
			snap.ConfigKeys[k] = v
		}
	}
	sql, err := exportDesktopCookies(desktopCookiesPath(root))
	if err != nil {
		return nil, err
	}
	snap.CookieSQL = sql
	return snap, nil
}

// ApplyDesktop writes a snapshot back over the live Desktop session. Desktop must
// be closed; the caller is expected to have checked, but this checks too because
// a write underneath a running app is silently lost.
func ApplyDesktop(root string, snap *DesktopSnapshot) error {
	if snap == nil {
		return errors.New("no Desktop snapshot to apply")
	}
	if !desktopSupported {
		return ErrDesktopUnsupported
	}
	if !dirExists(root) {
		return ErrNoDesktop
	}
	// Validate the snapshot BEFORE looking at machine state. A snapshot from
	// another host can never be applied, so reporting "Desktop is running" for it
	// would send the caller off to quit an app that would not have helped.
	host, _ := os.Hostname()
	if snap.Host != "" && host != "" && snap.Host != host {
		return fmt.Errorf(
			"this Desktop snapshot was captured on %q and cannot be used on %q: it is "+
				"sealed with the other machine's keychain key, so restoring it would "+
				"produce an unreadable session rather than a login.\n"+
				"Fix: sign in to Desktop once on this machine, then `clauderig account add`",
			snap.Host, host)
	}
	if desktopRunning() {
		return ErrDesktopRunning
	}
	if err := writeDesktopConfigKeys(root, snap.ConfigKeys); err != nil {
		return err
	}
	return importDesktopCookies(desktopCookiesPath(root), snap.CookieSQL)
}

// readDesktopConfig parses config.json into raw values, so every key we do not
// manage round-trips byte-for-byte.
func readDesktopConfig(root string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(desktopConfigPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	doc := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse Desktop config.json: %w", err)
	}
	return doc, nil
}

// writeDesktopConfigKeys sets the managed keys and REMOVES any managed key the
// snapshot doesn't have. The removal is the important half: leaving the previous
// account's `oauth:tokenCache` behind when the incoming account only has a V2
// entry would authenticate part of the app as the account we just switched away
// from.
func writeDesktopConfigKeys(root string, keys map[string]json.RawMessage) error {
	doc, err := readDesktopConfig(root)
	if err != nil {
		return err
	}
	for _, k := range desktopOAuthKeys {
		if v, ok := keys[k]; ok {
			doc[k] = v
		} else {
			delete(doc, k)
		}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(desktopConfigPath(root), append(out, '\n'), 0o600)
}

// cookieColumns lists the cookies table's columns, in declared order.
func cookieColumns(dbPath string) ([]string, error) {
	out, err := runSQLite(dbPath, "SELECT name FROM pragma_table_info('cookies');")
	if err != nil {
		return nil, err
	}
	var cols []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			cols = append(cols, l)
		}
	}
	if len(cols) == 0 {
		return nil, errors.New("cookies table has no columns")
	}
	return cols, nil
}

// exportDesktopCookies dumps the claude.ai rows as INSERT statements naming
// their columns explicitly. A missing DB is not an error — a Desktop that has
// never opened the web UI simply has no cookies to carry.
//
// Named columns rather than sqlite3's `.mode insert`, which emits positional
// `INSERT INTO cookies VALUES(…)`. Chromium changes this table between versions,
// and Desktop updates itself, so a snapshot taken before an update can be
// restored after one: positional values would then be misaligned or rejected.
// Naming the columns tolerates additions and reordering, and a column that has
// since disappeared is dropped on restore rather than failing the whole replay.
func exportDesktopCookies(dbPath string) ([]string, error) {
	if !fileExists(dbPath) {
		return nil, nil
	}
	if err := requireSQLite(); err != nil {
		return nil, err
	}
	cols, err := cookieColumns(dbPath)
	if err != nil {
		return nil, fmt.Errorf("read cookies schema: %w", err)
	}
	// Build each row as a complete INSERT naming its columns. quote() renders
	// every type — including BLOBs, as X'..' — in a form SQLite can re-read.
	var parts []string
	for _, c := range cols {
		parts = append(parts, "quote("+quoteIdent(c)+")")
	}
	sel := "SELECT 'INSERT INTO cookies (" + strings.Join(quoteIdents(cols), ",") + ") VALUES (' || " +
		strings.Join(parts, " || ',' || ") + " || ');' FROM cookies WHERE " + desktopCookieHosts + ";"
	out, err := runSQLite(dbPath, sel)
	if err != nil {
		return nil, fmt.Errorf("export Desktop cookies: %w", err)
	}
	var stmts []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "INSERT INTO") {
			stmts = append(stmts, line)
		}
	}
	return stmts, nil
}

// quoteIdent wraps a SQL identifier in double quotes, doubling any embedded one.
func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func quoteIdents(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = quoteIdent(s)
	}
	return out
}

// importDesktopCookies replaces the claude.ai rows with the snapshot's. The
// delete and the inserts run in one transaction so a failure can't leave the DB
// with the old account's cookies gone and the new account's not yet in.
//
// The DELETE runs whenever the DB exists, even with nothing to insert: an
// incoming account that simply has no claude.ai cookies must still displace the
// outgoing account's, or the app would keep authenticating its web UI as the
// account just switched away from — a half-switch that is worse than either end
// state. Conversely, having cookies to restore and no DB to restore them into is
// a failure, not a no-op, and must not be reported as success.
func importDesktopCookies(dbPath string, stmts []string) error {
	if !fileExists(dbPath) {
		if len(stmts) > 0 {
			return fmt.Errorf("Desktop cookie database %s is missing, so the web session cannot be restored", dbPath)
		}
		return nil
	}
	if err := requireSQLite(); err != nil {
		return err
	}
	script := "BEGIN IMMEDIATE;\nDELETE FROM cookies WHERE " + desktopCookieHosts + ";\n"
	if len(stmts) > 0 {
		script += strings.Join(stmts, "\n") + "\n"
	}
	script += "COMMIT;\n"
	if _, err := runSQLite(dbPath, script); err != nil {
		return fmt.Errorf("restore Desktop cookies: %w", err)
	}
	return nil
}

func requireSQLite() error {
	if fileExists(sqlite3Bin) {
		return nil
	}
	return fmt.Errorf("%s is required to carry Desktop's session cookies and was not found", sqlite3Bin)
}

func runSQLite(dbPath string, args ...string) (string, error) {
	cmd := exec.Command(sqlite3Bin, append([]string{dbPath}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// --- store plumbing -------------------------------------------------------

func (s *Store) desktopPath(id string) string {
	return filepath.Join(s.acctDir(id), "desktop-session.json")
}

// SaveDesktop stores an account's Desktop snapshot (0600 — it is ciphertext, but
// it is still a session).
func (s *Store) SaveDesktop(id string, snap *DesktopSnapshot) error {
	if snap == nil {
		return nil
	}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.acctDir(id), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.desktopPath(id), append(b, '\n'), 0o600)
}

// Desktop reads back a stored snapshot; (nil, nil) when the account has none.
func (s *Store) Desktop(id string) (*DesktopSnapshot, error) {
	b, err := os.ReadFile(s.desktopPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snap DesktopSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, fmt.Errorf("parse stored Desktop snapshot for %s: %w", id, err)
	}
	return &snap, nil
}
