package auth

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/rigsmith/rigsmith/core/secrets"
)

// resolveOpRef resolves a 1Password secret reference ("op://vault/item/field")
// via the `op` CLI, mapping the common failure modes to actionable errors
// instead of a raw non-zero exit: `op` not installed, not signed in, or the
// reference not found.
func resolveOpRef(ctx context.Context, ref string) (string, error) {
	if _, err := exec.LookPath("op"); err != nil {
		return "", fmt.Errorf("1Password CLI `op` not found on PATH — install it "+
			"(https://developer.1password.com/docs/cli/) or use an env:/cmd: ref instead (ref %q)", ref)
	}
	out, stderr, err := runOp(ctx, "read", ref)
	if err != nil {
		return "", classifyOpError(ref, stderr, err)
	}
	if out == "" {
		return "", fmt.Errorf("1Password ref %q resolved to an empty value", ref)
	}
	return out, nil
}

// runOp runs `op <args>` and returns trimmed stdout, raw stderr, and the run
// error. Keeping stdout and stderr separate ensures the resolved secret (stdout)
// is never contaminated by diagnostic text.
func runOp(ctx context.Context, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, "op", args...)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return strings.TrimSpace(out.String()), errBuf.String(), err
}

// classifyOpError turns an `op` failure into a hint the operator can act on,
// keying off the message `op` wrote to stderr.
func classifyOpError(ref, stderr string, runErr error) error {
	low := strings.ToLower(stderr)
	switch {
	case containsAny(low, "not currently signed in", "not signed in", "no account", "no accounts",
		"session expired", "session ", "authorization required", "op_service_account_token", "signin"):
		return fmt.Errorf("not signed in to 1Password — run `op signin` locally, "+
			"or set OP_SERVICE_ACCOUNT_TOKEN in CI (resolving ref %q)", ref)
	case containsAny(low, "isn't an item", "no item matching", "could not find", "not found",
		"isn't a valid secret reference", "no vault matching", "doesn't exist"):
		return fmt.Errorf("1Password secret reference %q not found — check the vault/item/field "+
			"(stderr: %s)", ref, strings.TrimSpace(stderr))
	default:
		return fmt.Errorf("1Password `op read %s` failed: %w: %s", ref, runErr, strings.TrimSpace(stderr))
	}
}

// PreflightRef performs a non-destructive readiness check for a configured auth
// ref, for the release wizard. It never resolves the actual secret (no side
// effects, no biometric prompt for the value): for op:// it confirms `op` is
// installed and a session exists; for env: it confirms the variable is set; for
// cmd: it can only report that the command runs at publish time.
func PreflightRef(ctx context.Context, ref string) (detail string, ok bool) {
	switch {
	case strings.HasPrefix(ref, "op://"):
		if _, err := exec.LookPath("op"); err != nil {
			return "1Password CLI `op` not found on PATH", false
		}
		// `op whoami` succeeds for both an interactive session and a service
		// account, and fails when signed out — without reading the secret.
		if _, stderr, err := runOp(ctx, "whoami"); err != nil {
			_ = stderr
			return "1Password: not signed in — run `op signin` (or set OP_SERVICE_ACCOUNT_TOKEN)", false
		}
		return "1Password CLI signed in", true
	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimPrefix(ref, "env:")
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return "env:" + name + " is set", true
		}
		return "env:" + name + " is not set", false
	case strings.HasPrefix(ref, "cmd:"):
		return "command ref — resolved at publish time", true
	default:
		if provider, name, ok := secrets.ParseRef(ref); ok {
			return preflightManagerRef(ctx, provider, name)
		}
		return fmt.Sprintf("unrecognized ref %q (want op://… | env:NAME | cmd:… | doppler:NAME)", ref), false
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// preflightManagerRef checks a "doppler:NAME" ref the way the op:// check
// does: that there is a release config to read and the CLI is signed in,
// without reading the secret.
func preflightManagerRef(ctx context.Context, provider, name string) (string, bool) {
	r := secrets.ReaderFrom(ctx)
	switch {
	case r == nil:
		return fmt.Sprintf("%s:%s — no %q block in .rig.json", provider, name, provider), false
	case !r.Available():
		return fmt.Sprintf("%s:%s — .rig.json sets no %s.release config", provider, name, provider), false
	case secrets.InCI():
		return fmt.Sprintf("%s:%s — read from the job environment in CI", provider, name), true
	}
	if err := r.Ready(ctx); err != nil {
		return err.Error(), false
	}
	return r.Provider().Title() + " CLI signed in", true
}
