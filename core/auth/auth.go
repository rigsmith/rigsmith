// Package auth resolves the registry credential a publish step needs, from one
// of several sources, behind a single precedence policy. It is the general seam
// every ecosystem shares: the policy (which source wins) lives here, while the
// registry-specific bits (npm's OIDC exchange endpoint, the credentials-file
// format) live in the adapters.
//
// Precedence, first match wins:
//
//  1. OIDC trusted publishing — a CI OIDC identity exchanged for an ephemeral
//     token. (Added in a later slice; the seam is shaped for it now.)
//  2. A configured secret reference — "op://…" (1Password), "env:NAME", or
//     "cmd:…" (a shell command whose stdout is the token). Resolved just-in-time
//     so a time-limited secret stays fresh.
//  3. A fallback environment variable (e.g. NPM_TOKEN).
//  4. Nothing — the caller falls back to the package manager's ambient
//     credential (~/.npmrc and friends), i.e. today's behaviour.
//
// A resolved token is registered with the caller's Masker so it never appears
// in logs, and is returned to the caller; the caller hands it to the adapter,
// which renders its own ecosystem-native credentials file.
package auth

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Method names the mechanism that produced a credential.
type Method string

const (
	// MethodNone means no credential was resolved; use the ambient one.
	MethodNone Method = "none"
	// MethodEnv means the token came from an environment variable.
	MethodEnv Method = "env"
	// MethodRef means the token came from a secret-manager reference/command.
	MethodRef Method = "secret-ref"
	// MethodOIDC means the token was minted via OIDC trusted publishing.
	MethodOIDC Method = "oidc"
)

// Credential is a resolved registry credential.
type Credential struct {
	// Token is the bearer token to authenticate the publish. Empty when
	// Method is MethodNone.
	Token string
	// Method records how Token was obtained, for reporting.
	Method Method
	// Provenance asks the adapter to attach a provenance attestation when its
	// toolchain supports it. Only OIDC sets this today.
	Provenance bool
}

// Resolved reports whether a usable token was produced.
func (c Credential) Resolved() bool { return c.Token != "" }

// Masker accepts secret values to redact from output. *Redactor implements it;
// so does the pipeline's SecretMasker.
type Masker interface{ Add(string) }

// Request describes how to resolve a credential for one registry.
type Request struct {
	// Ref is a configured secret reference: "op://…", "env:NAME", or "cmd:…".
	// Empty skips the secret-ref step.
	Ref string
	// FallbackEnv is an environment variable read when Ref is empty (e.g.
	// "NPM_TOKEN"). Empty skips the fallback step.
	FallbackEnv string
	// Masker, when set, receives any resolved token so it is redacted.
	Masker Masker
}

// Resolve walks the precedence chain and returns the first credential it finds.
// A MethodNone credential (no error) means "nothing configured — use ambient".
func Resolve(ctx context.Context, req Request) (Credential, error) {
	if ref := strings.TrimSpace(req.Ref); ref != "" {
		token, method, err := resolveRef(ctx, ref)
		if err != nil {
			return Credential{}, err
		}
		if token == "" {
			return Credential{}, fmt.Errorf("auth ref %q resolved to an empty token", ref)
		}
		mask(req.Masker, token)
		return Credential{Token: token, Method: method}, nil
	}

	if env := strings.TrimSpace(req.FallbackEnv); env != "" {
		if token := strings.TrimSpace(os.Getenv(env)); token != "" {
			mask(req.Masker, token)
			return Credential{Token: token, Method: MethodEnv}, nil
		}
	}

	return Credential{Method: MethodNone}, nil
}

// resolveRef interprets a secret reference and returns its token plus the method
// that produced it.
func resolveRef(ctx context.Context, ref string) (string, Method, error) {
	switch {
	case strings.HasPrefix(ref, "op://"):
		// 1Password secret reference, resolved via the op CLI (with actionable
		// errors for the missing/signed-out/not-found cases).
		out, err := resolveOpRef(ctx, ref)
		return out, MethodRef, err

	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimPrefix(ref, "env:")
		return strings.TrimSpace(os.Getenv(name)), MethodEnv, nil

	case strings.HasPrefix(ref, "cmd:"):
		// Arbitrary command whose stdout is the token. Run through the shell so
		// the user can write a pipeline (e.g. "cmd:op item get npm --otp").
		out, err := runToken(ctx, "sh", "-c", strings.TrimPrefix(ref, "cmd:"))
		return out, MethodRef, err

	default:
		return "", MethodNone, fmt.Errorf("unrecognized auth ref %q: want op://…, env:NAME, or cmd:…", ref)
	}
}

// runToken runs a capture command and returns its trimmed stdout. stderr is
// surfaced on failure but never mixed into the token (a capture command must
// write only the secret to stdout).
func runToken(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("auth command %q failed: %w: %s", name, err, strings.TrimSpace(errBuf.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func mask(m Masker, token string) {
	if m != nil {
		m.Add(token)
	}
}
