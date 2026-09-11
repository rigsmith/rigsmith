package configcodec

import (
	"context"
	"path/filepath"
	"strings"
)

// Check selected endpoint declarations after native profile inheritance. An
// omitted proxy address retains its ancestor; socket maps replace matching raw
// keys only. No address resolution, path lookup or socket connection takes place.
func validateInheritedNetworkEndpoints(ctx context.Context, chain []map[string]any) error {
	addresses := make(map[string]string)
	sockets := make(map[string]string)
	for i := len(chain) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		network := profileMap(chain[i]["network"])
		for _, key := range []string{"proxy_url", "socks_url"} {
			if value, exists := network[key]; exists {
				addresses[key] = value.(string)
			}
		}
		for path, value := range profileMap(network["unix_sockets"]) {
			if err := ctx.Err(); err != nil {
				return err
			}
			sockets[path] = value.(string)
		}
	}
	// Absent addresses use valid native defaults. resolve_runtime parses both
	// explicit addresses even when SOCKS is disabled. This is only its mandatory
	// nonblank check, not a substitute for the native URL parser and fallback.
	for _, address := range addresses {
		if strings.TrimSpace(address) == "" {
			return ErrValidation
		}
	}
	for path, permission := range sockets {
		if err := ctx.Err(); err != nil {
			return err
		}
		if permission != "allow" {
			continue
		}
		// Native allowlists accept Unix-style absolute paths on every platform,
		// plus host-native absolute paths. Use destination Go path syntax as in
		// header source validation. NUL refusal is an explicit restore policy.
		if strings.ContainsRune(path, '\x00') || !strings.HasPrefix(path, "/") && !filepath.IsAbs(path) {
			return ErrValidation
		}
	}
	return ctx.Err()
}
