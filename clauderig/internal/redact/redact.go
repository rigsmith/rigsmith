// Package redact strips secrets from Claude Code config before it is committed to
// the sync repo, and scans for any that slip past (the tripwire). Redaction is an
// always-on transform: secret-bearing fields are replaced with a sentinel so
// restore can tell "this was redacted — keep the local machine's value" rather
// than clobbering it with a placeholder. Secrets are never synced; a new machine
// re-authenticates (the strip-don't-sync model).
package redact

import (
	"encoding/json"
	"sort"
	"strings"
)

// Placeholder marks a value that was redacted out. Restore treats a field whose
// synced value equals Placeholder as "leave the local value untouched".
const Placeholder = "__CLAUDERIG_REDACTED__"

// Policy decides which JSON fields hold secrets.
type Policy struct {
	// SecretKeys are key names (case-insensitive) whose scalar string value is
	// always a secret (token, password, authorization, …).
	SecretKeys map[string]bool
	// SecretContainers are key names (case-insensitive) whose value is an object
	// that is a *bucket* of credentials — every string leaf under it is redacted
	// (env, headers).
	SecretContainers map[string]bool
}

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[strings.ToLower(w)] = true
	}
	return m
}

// DefaultPolicy covers the secret-bearing shapes Claude Code config can carry:
// inline API keys/tokens by key name, and env/headers buckets (MCP servers,
// settings env) by container.
func DefaultPolicy() Policy {
	return Policy{
		SecretKeys: set(
			"token", "accesstoken", "access_token", "refreshtoken", "refresh_token",
			"apikey", "api_key", "authtoken", "auth_token", "password", "passwd",
			"secret", "clientsecret", "client_secret", "authorization", "auth",
			"bearer", "x-api-key", "anthropic_api_key", "openai_api_key",
		),
		SecretContainers: set("env", "headers"),
	}
}

// Redact returns a deep-redacted copy of v (parsed JSON: map/slice/scalar) and the
// sorted list of dotted paths that were redacted. v is not mutated.
func Redact(v any, p Policy) (any, []string) {
	var paths []string
	out := redactNode(v, "", false, p, &paths)
	sort.Strings(paths)
	return out, paths
}

// RedactBytes redacts a JSON document, returning indented JSON. Keys are emitted
// in Go's deterministic (sorted) order, so output is stable across syncs.
func RedactBytes(data []byte, p Policy) (redacted []byte, paths []string, err error) {
	var v any
	if err = json.Unmarshal(data, &v); err != nil {
		return nil, nil, err
	}
	out, paths := Redact(v, p)
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return append(b, '\n'), paths, nil
}

func redactNode(node any, path string, redactAllStrings bool, p Policy, paths *[]string) any {
	switch n := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, v := range n {
			child := joinPath(path, k)
			kl := strings.ToLower(k)
			switch {
			case p.SecretKeys[kl] && isScalar(v):
				out[k] = Placeholder
				*paths = append(*paths, child)
			default:
				out[k] = redactNode(v, child, redactAllStrings || p.SecretContainers[kl], p, paths)
			}
		}
		return out
	case []any:
		out := make([]any, len(n))
		for i, v := range n {
			out[i] = redactNode(v, joinIndex(path, i), redactAllStrings, p, paths)
		}
		return out
	case string:
		if redactAllStrings {
			*paths = append(*paths, path)
			return Placeholder
		}
		return n
	default:
		return node
	}
}

func isScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	default:
		return true
	}
}

func joinPath(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

func joinIndex(base string, i int) string {
	return base + "[" + itoa(i) + "]"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
