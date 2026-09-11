// Package configcodec prepares Codex TOML configuration for future capture and
// restore. It is a byte codec only: it neither accesses files nor runs commands.
package configcodec

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/rigsmith/rigsmith/internal/agentrig/secrets"
)

// MaxBytes bounds both input documents and encoded results independently.
const MaxBytes = 1 << 20
const maxDepth = 64

var (
	ErrSize         = errors.New("Codex TOML exceeds the configuration size limit")
	ErrSyntax       = errors.New("invalid Codex TOML configuration")
	ErrDepth        = errors.New("Codex TOML exceeds the nesting limit")
	ErrSecret       = errors.New("Codex TOML contains a possible credential outside protected fields")
	ErrUnsafeBackup = errors.New("Codex TOML backup contains local-only fields")
	ErrPath         = errors.New("Codex TOML contains an unclassified local path or environment reference")
	ErrEncode       = errors.New("cannot encode Codex TOML configuration")
)

// Capture parses TOML, omits local-only fields, and refuses recognizable secrets
// in remaining keys or values. Comments and formatting are not copied. Unknown
// non-secret fields without explicit local references and native TOML scalar
// types are preserved. Arrays containing any omitted field are omitted as a unit;
// secrets are never matched by index.
// Errors deliberately contain no source text, field names, or parser excerpts.
func Capture(source []byte) ([]byte, error) {
	doc, err := parse(source)
	if err != nil {
		return nil, err
	}
	clean, _, err := sanitize(doc, nil, 0)
	if err != nil {
		return nil, err
	}
	return encode(clean)
}

// Restore overlays a sanitized backup on local TOML, keeping local-only fields
// and unknown local additions. It rejects unsanitized backups rather than
// importing credentials. A local MCP server, model-provider, or agent entry
// containing protected values is kept whole: changing its identity while retaining
// local credentials or file references is unsafe. A local array with protected
// descendants is likewise kept whole. A nil local document denotes a fresh machine; omitted fields stay
// absent and no placeholder is ever written. Output still requires the caller's
// config validation and guarded file replacement before use.
func Restore(backup, local []byte) ([]byte, error) {
	remote, err := parse(backup)
	if err != nil {
		return nil, err
	}
	_, omitted, err := sanitize(remote, nil, 0)
	if err != nil {
		return nil, err
	}
	if omitted {
		return nil, ErrUnsafeBackup
	}
	current, err := parse(local)
	if err != nil {
		return nil, err
	}
	// Local credentials are allowed. Check resource limits without applying the
	// publication tripwire to values that must remain on this machine.
	if err := checkDepth(current, 0); err != nil {
		return nil, err
	}
	merged := merge(remote, current, nil)
	return encode(merged)
}

func parse(data []byte) (map[string]any, error) {
	if len(data) > MaxBytes {
		return nil, ErrSize
	}
	if err := preflight(data); err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, ErrSyntax
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

func encode(doc any) ([]byte, error) {
	data, err := toml.Marshal(doc)
	if err != nil {
		return nil, ErrEncode
	}
	if len(data) > MaxBytes {
		return nil, ErrSize
	}
	return data, nil
}

func sanitize(node any, path []string, depth int) (any, bool, error) {
	if depth > maxDepth {
		return nil, false, ErrDepth
	}
	switch value := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		omitted := false
		for key, child := range value {
			// Keys themselves are published even if their values would be omitted.
			// Reject suspect keys; diagnostics never repeat them.
			if suspect(key) {
				return nil, false, ErrSecret
			}
			if hasLocalReference(key) {
				return nil, false, ErrPath
			}
			if referenceHeaders(path) {
				name, ok := child.(string)
				if !ok || !envName.MatchString(name) {
					return nil, false, ErrSyntax
				}
			}
			if protected(path, key) {
				omitted = true
				continue
			}
			clean, removed, err := sanitize(child, descend(path, key), depth+1)
			if err != nil {
				return nil, false, err
			}
			omitted = omitted || removed
			if _, array := child.([]any); array && removed {
				continue
			}
			out[key] = clean
		}
		return out, omitted, nil
	case []any:
		out := make([]any, len(value))
		omitted := false
		for i, child := range value {
			clean, removed, err := sanitize(child, path, depth+1)
			if err != nil {
				return nil, false, err
			}
			out[i] = clean
			omitted = omitted || removed
		}
		return out, omitted, nil
	case string:
		if suspect(value) {
			return nil, false, ErrSecret
		}
		if hasLocalReference(value) {
			return nil, false, ErrPath
		}
	}
	return node, false, nil
}

func descend(path []string, key string) []string {
	out := make([]string, len(path)+1)
	copy(out, path)
	out[len(path)] = key
	return out
}

func checkDepth(node any, depth int) error {
	if depth > maxDepth {
		return ErrDepth
	}
	switch value := node.(type) {
	case map[string]any:
		for _, child := range value {
			if err := checkDepth(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := checkDepth(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func hasProtected(node any, path []string) bool {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if protected(path, key) || suspect(key) || hasLocalReference(key) || hasProtected(child, descend(path, key)) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if hasProtected(child, path) {
				return true
			}
		}
	case string:
		return suspect(value) || hasLocalReference(value)
	}
	return false
}

func merge(remote, local any, path []string) any {
	// Keyed integration and agent entries have identity; array positions do not.
	// Neither is a safe place to graft local credentials or file references onto
	// changed remote configuration.
	integration := len(path) == 2 && (path[0] == "mcp_servers" || path[0] == "model_providers" || path[0] == "agents")
	_, localArray := local.([]any)
	if (integration || localArray) && hasProtected(local, path) {
		return local
	}
	rm, remoteMap := remote.(map[string]any)
	lm, localMap := local.(map[string]any)
	if remoteMap && localMap {
		out := make(map[string]any, len(lm)+len(rm))
		for key, value := range lm {
			out[key] = value
		}
		for key, value := range rm {
			lv, exists := lm[key]
			if exists {
				out[key] = merge(value, lv, descend(path, key))
			} else {
				out[key] = value
			}
		}
		return out
	}
	// A type change must not erase a local protected subtree or recognizable
	// credential, even when the backup substitutes a scalar for a whole table.
	if hasProtected(local, path) {
		return local
	}
	return remote
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func referenceHeaders(path []string) bool {
	return len(path) == 3 && (path[0] == "mcp_servers" || path[0] == "model_providers") && path[2] == "env_http_headers"
}

var keyNormalizer = strings.NewReplacer("_", "", "-", "", ".", "")

func protected(path []string, key string) bool {
	// Documented header maps here contain environment variable names, not values.
	if referenceHeaders(path) {
		return false
	}
	if localPathField(path, key) {
		return true
	}
	k := keyNormalizer.Replace(strings.ToLower(key))
	switch k {
	// Buckets may contain short or numeric credentials that shape scanning cannot
	// recognize. Keep the entire value local, irrespective of TOML type.
	case "env", "headers", "httpheaders", "queryparams", "credentials", "oauth", "auth",
		"authorization", "command", "args", "httpheadershelper", "notify", "hooks":
		return true
	}
	for _, suffix := range []string{"token", "secret", "password", "passwd", "apikey", "privatekey", "secretkey", "accesskey", "signingkey", "bearer", "tokencache"} {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	// These are machine-specific locations or trust decisions. No global string
	// replacement of arbitrary paths is attempted by this first codec.
	if len(path) == 0 {
		switch k {
		case "projects", "sqlitehome", "logdir", "codexhome", "cliauthcredentialsstore", "mcpoauthcredentialsstore":
			return true
		}
	}
	return false
}

// URL userinfo and query/fragment payloads can hold credentials too short for a
// signature detector. Refuse them even under an unknown field or inside prose.
// A later adapter may support explicit non-secret query parameters; this codec
// deliberately has no raw-string exception for them.
var urlInText = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`)

func suspect(value string) bool {
	if secrets.Contains(value) {
		return true
	}
	for _, raw := range urlInText.FindAllString(value, -1) {
		u, err := url.Parse(raw)
		if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return true
		}
	}
	return false
}
