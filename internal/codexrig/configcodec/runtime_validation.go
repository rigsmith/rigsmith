package configcodec

import (
	"context"
	"math"
	"strings"
)

// These checks run only after the pinned schema has established value types.
// They mirror selected pure runtime rules, not destination or startup readiness.
// Provenance: schema/README.md, Codex release 0.144.6.
func validateRuntimeConfig(ctx context.Context, doc map[string]any) error {
	if err := validateProviderConfig(ctx, doc); err != nil {
		return err
	}
	servers, _ := doc["mcp_servers"].(map[string]any)
	for _, value := range servers {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Native deserialization checks disabled servers too.
		if err := validateMCPConfig(value.(map[string]any)); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func validateProviderConfig(ctx context.Context, doc map[string]any) error {
	providers, _ := doc["model_providers"].(map[string]any)
	// config_toml::validate_model_providers runs before the native catalog merge.
	// Its reserved-name refusal takes precedence over merge's or_insert behavior.
	for key, value := range providers {
		if err := ctx.Err(); err != nil {
			return err
		}
		if key == "amazon-bedrock" {
			continue
		}
		if isBuiltinProvider(key) {
			return ErrValidation
		}
		provider := value.(map[string]any)
		name, _ := provider["name"].(string)
		if strings.TrimSpace(name) == "" || hasConfigKey(provider, "aws") {
			return ErrValidation
		}
		if auth, exists := provider["auth"].(map[string]any); exists {
			if strings.TrimSpace(auth["command"].(string)) == "" || provider["requires_openai_auth"] == true ||
				hasAnyConfigKey(provider, "env_key", "experimental_bearer_token") {
				return ErrValidation
			}
		}
	}
	if value, exists := providers["amazon-bedrock"]; exists {
		// Native catalog construction checks this entry even when unselected.
		// AWS profile/region may be overridden; all other fields must equal the
		// Rust ModelProviderInfo default, including explicitly present defaults.
		for key, value := range value.(map[string]any) {
			switch key {
			case "aws":
			case "name":
				if value != "" {
					return ErrValidation
				}
			case "wire_api":
				if value != "responses" {
					return ErrValidation
				}
			case "requires_openai_auth", "supports_websockets":
				if value != false {
					return ErrValidation
				}
			default:
				// Option fields are non-default even when empty or zero.
				return ErrValidation
			}
		}
	}
	return nil
}

func validateMCPConfig(server map[string]any) error {
	// Rust Duration::try_from_secs_f64 accepts finite nonnegative seconds
	// below 2^64, not Go's much narrower int64 nanosecond duration range.
	for _, key := range []string{"startup_timeout_sec", "tool_timeout_sec"} {
		if value, exists := server[key]; exists {
			var seconds float64
			switch n := value.(type) {
			case int64:
				seconds = float64(n)
			case float64:
				seconds = n
			}
			if seconds < 0 || seconds >= math.Ldexp(1, 64) {
				return ErrValidation
			}
		}
	}
	if hasConfigKey(server, "command") {
		if hasAnyConfigKey(server, "url", "bearer_token_env_var", "http_headers", "env_http_headers", "oauth", "oauth_resource", "auth") {
			return ErrValidation
		}
		envVars, _ := server["env_vars"].([]any)
		for _, value := range envVars {
			if spec, ok := value.(map[string]any); ok {
				if source, exists := spec["source"]; exists && source != "local" && source != "remote" {
					return ErrValidation
				}
			}
		}
	} else if hasConfigKey(server, "url") {
		if hasAnyConfigKey(server, "args", "env", "env_vars", "cwd") {
			return ErrValidation
		}
	} else {
		return ErrValidation
	}
	return nil
}

func hasConfigKey(doc map[string]any, key string) bool {
	_, exists := doc[key]
	return exists
}

func hasAnyConfigKey(doc map[string]any, keys ...string) bool {
	for _, key := range keys {
		if hasConfigKey(doc, key) {
			return true
		}
	}
	return false
}
