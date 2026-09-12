package hooks

import (
	"context"
	"fmt"
	"strings"

	"github.com/rigsmith/rigsmith/internal/codexrig/appserver"
)

// A hook Codex has not been told to trust does not run. It does not error and it
// does not warn — `hooks/list` simply reports trustStatus "untrusted" — which
// makes an installed-but-untrusted hook the quietest possible way for a backup
// tool to stop backing anything up.
//
// The trust record lives in config.toml as
//
//	[hooks.state."<abs hooks.json path>:<snake_event>:<group>:<handler>"]
//	trusted_hash = "sha256:…"
//
// and both halves are Codex's: it decides the key, and it decides what the hash
// is of. So Trust asks — it reads the hash Codex reports and hands it straight
// back, and it makes the edit THROUGH Codex so config.toml keeps whatever
// formatting Codex keeps rather than surviving a TOML round trip of ours.
//
// This is still the user granting trust to a file, so it is never automatic:
// `hooks install` prints what to run, and this only happens when somebody runs
// it.

// TrustResult is what Trust did.
type TrustResult struct {
	Trusted []string // event names now trusted
	Already []string // event names that already were
	// Foreign names hooks that are not codexrig's. Reported rather than
	// touched: trusting somebody else's hook is not this command's business.
	Foreign []string
}

// Trust records codexrig's hooks as trusted in Codex's config.
//
// home is the Codex home to act on (empty = the machine's own), and cwd is the
// directory Codex resolves project hooks from — the app-server uses its own
// working directory and ignores a cwd parameter.
func Trust(ctx context.Context, home, cwd string) (TrustResult, error) {
	var out TrustResult
	client, err := appserver.Start(ctx, home, cwd)
	if err != nil {
		return out, err
	}
	defer client.Close()

	list, err := client.ListHooks(ctx)
	if err != nil {
		return out, err
	}
	for _, h := range list {
		if !strings.Contains(h.Command, Marker) {
			if !h.Trusted() {
				out.Foreign = append(out.Foreign, h.EventName)
			}
			continue
		}
		if h.Trusted() {
			out.Already = append(out.Already, h.EventName)
			continue
		}
		if h.CurrentHash == "" {
			return out, fmt.Errorf("codex reported no hash for the %s hook, so there is nothing to record", h.EventName)
		}
		// The key is Codex's own, quoted as a single TOML key: it contains
		// slashes, dots and colons, none of which may be read as path
		// separators in the dotted keyPath.
		keyPath := `hooks.state."` + h.Key + `".trusted_hash`
		if err := client.WriteConfig(ctx, keyPath, h.CurrentHash); err != nil {
			return out, fmt.Errorf("recording trust for the %s hook: %w", h.EventName, err)
		}
		out.Trusted = append(out.Trusted, h.EventName)
	}
	return out, nil
}

// Check reports how Codex currently sees codexrig's hooks, without changing
// anything.
func Check(ctx context.Context, home, cwd string) ([]appserver.HookInfo, error) {
	client, err := appserver.Start(ctx, home, cwd)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	list, err := client.ListHooks(ctx)
	if err != nil {
		return nil, err
	}
	var ours []appserver.HookInfo
	for _, h := range list {
		if strings.Contains(h.Command, Marker) {
			ours = append(ours, h)
		}
	}
	return ours, nil
}
