// Package sign resolves the optional code-signing/notarization secrets a desktop
// build (Tauri, Electron) needs, reusing the core/auth precedence seam. It is the
// build-time analogue of how the publish step resolves a registry credential:
// each configured environment variable maps to a secret reference ("op://…",
// "env:NAME", "cmd:…") that is resolved just-in-time and masked, then handed to
// the adapter to expose to the build tool.
//
// Signing is opt-in. With it disabled (the default), ResolveEnv returns nil and
// the build runs unsigned — no secrets are read.
package sign

import (
	"context"
	"fmt"
	"sort"

	"github.com/rigsmith/rigsmith/core/auth"
)

// ResolveEnv resolves each ENV_VAR -> secret-reference entry in refs to its value
// via the auth precedence chain, masking every resolved value. It returns nil
// when refs is empty (nothing to sign with). A reference that errors or resolves
// to an empty value fails the whole resolution, so a misconfigured signing setup
// is surfaced rather than silently producing an unsigned build.
func ResolveEnv(ctx context.Context, refs map[string]string, masker auth.Masker) (map[string]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	// Resolve in a stable order so failures are deterministic.
	keys := make([]string, 0, len(refs))
	for k := range refs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]string, len(refs))
	for _, k := range keys {
		cred, err := auth.Resolve(ctx, auth.Request{Ref: refs[k], Masker: masker})
		if err != nil {
			return nil, fmt.Errorf("signing secret %s: %w", k, err)
		}
		if !cred.Resolved() {
			return nil, fmt.Errorf("signing secret %s: reference %q resolved to an empty value", k, refs[k])
		}
		out[k] = cred.Token
	}
	return out, nil
}
