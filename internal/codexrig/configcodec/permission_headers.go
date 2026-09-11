package configcodec

import (
	"context"
	"path/filepath"
	"strings"
)

// Codex's pinned rama-http dependency reexports http 1.4.0 HeaderName: a
// nonempty ASCII token, up to 65535 bytes. Do not trim whitespace or accept
// Unicode letters. This checks declarations only, never secret/header values.
func validNetworkHeaderName(name string) bool {
	if len(name) == 0 || len(name) > 65535 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		default:
			return false
		}
	}
	return true
}

// Validate only actions referenced by effective hooks. Native conversion does
// not execute unused actions. A child action declaration replaces both operation
// vectors, including a vector omitted by that child (native default: empty).
func validateNetworkActionHeaders(ctx context.Context, action map[string]any) error {
	strip, _ := action["strip_request_headers"].([]any)
	for _, value := range strip {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !validNetworkHeaderName(value.(string)) {
			return ErrValidation
		}
	}
	inject, _ := action["inject_request_headers"].([]any)
	for _, value := range inject {
		if err := ctx.Err(); err != nil {
			return err
		}
		header := profileMap(value)
		name, _ := header["name"].(string) // Native omission defaults to an invalid empty name.
		if !validNetworkHeaderName(name) {
			return ErrValidation
		}
		env, hasEnv := header["secret_env_var"].(string)
		path, hasFile := header["secret_file"].(string)
		if hasEnv == hasFile {
			return ErrValidation
		}
		if hasEnv {
			// Restore policy: an environment name containing NUL cannot resolve.
			if strings.TrimSpace(env) == "" || strings.IndexByte(env, 0) >= 0 {
				return ErrValidation
			}
		} else {
			// Destination-local declaration check using this host's path syntax.
			// No expansion, normalization, stat, environment lookup or file read.
			// NUL refusal is restore policy: no local file can satisfy that source.
			if strings.TrimSpace(path) == "" || strings.IndexByte(path, 0) >= 0 || !filepath.IsAbs(path) {
				return ErrValidation
			}
		}
	}
	return ctx.Err()
}
