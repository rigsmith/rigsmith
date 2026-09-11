package configcodec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestPinnedConfigSchema(t *testing.T) {
	if got := fmt.Sprintf("%x", sha256.Sum256(configSchema)); got != "841e0ab1c1bd2fea736ba2d46212ab5bedc06dce9fd83bbafbf50b57b9056d17" {
		t.Fatal("release schema changed without auditing provenance", got)
	}
	var doc any
	if err := json.Unmarshal(configSchema, &doc); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(v any) {
		switch value := v.(type) {
		case map[string]any:
			if ref, ok := value["$ref"].(string); ok && !strings.HasPrefix(ref, "#/") {
				t.Fatal("external schema reference")
			}
			if value["type"] == "integer" && value["format"] == nil {
				t.Fatal("integer field lacks native numeric format")
			}
			for _, child := range value {
				walk(child)
			}
		case []any:
			for _, child := range value {
				walk(child)
			}
		}
	}
	walk(doc)
}

func TestValidateConfigSet(t *testing.T) {
	tests := []struct {
		name, base string
		profiles   []string
		valid      bool
	}{
		{"defaults", "", nil, true},
		{"basic", "model='example-model'\nmodel_reasoning_effort='high'\napproval_policy='on-request'", nil, true},
		{"private local provider", "model_provider='private'\n[model_providers.private]\nname='Private'\nexperimental_bearer_token='local-private-value'", nil, true},
		{"profile inherits provider", "[model_providers.custom]\nname='Custom'\nbase_url='https://example.com/v1'", []string{"model_provider='custom'\n[model_providers.custom]\nname='Profile'"}, true},
		{"profile may change type incorrectly", "model='base'", []string{"model=123"}, false},
		{"profiles independent", "", []string{"[model_providers.custom]\nname='Custom'", "model_provider='custom'"}, false},
		{"unknown root key", "unrecognized_setting=true", nil, false},
		{"unknown nested key", "[history]\nmade_up=true", nil, false},
		{"wrong type", "model=true", nil, false},
		{"wrong enum", "model_reasoning_summary='made-up'", nil, false},
		{"model-defined reasoning effort", "model_reasoning_effort='custom-effort'", nil, true},
		{"memory alias", "[memories]\nno_memories_if_mcp_or_web_search=true", nil, true},
		{"invalid memory alias", "[memories]\nno_memories_if_mcp_or_web_search='wrong'", nil, false},
		{"canonical memory key wins", "[memories]\nno_memories_if_mcp_or_web_search='ignored'\ndisable_on_external_context=true", nil, true},
		{"legacy selector", "profile='work'", nil, false},
		{"legacy profiles", "[profiles.work]\nmodel='example'", nil, false},
		{"legacy profile in overlay", "", []string{"[profiles.work]\nmodel='example'"}, false},
		{"missing provider", "model_provider='missing'", nil, false},
		{"fractional integer", "model_context_window=1.5", nil, false},
		{"float typed integer", "model_context_window=1.0", nil, false},
		{"uint16 overflow", "mcp_oauth_callback_port=65536", nil, false},
		{"uint16 boundary", "mcp_oauth_callback_port=65535", nil, true},
		{"negative unsigned", "mcp_oauth_callback_port=-1", nil, false},
		{"int32 overflow", "[agents]\nmax_depth=2147483648", nil, false},
		{"integer boundary", "model_context_window=9223372036854775807", nil, true},
		{"date is not string", "model=2026-01-01", nil, false},
		{"nonfinite", "[mcp_servers.demo]\nurl='https://example.com/mcp'\ntool_timeout_sec=nan", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profiles := make([][]byte, len(tc.profiles))
			for i, data := range tc.profiles {
				profiles[i] = []byte(data)
			}
			err := ValidateConfigSet(t.Context(), SupportedConfigVersion, []byte(tc.base), profiles)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
			if err != nil && strings.Contains(err.Error(), "local-private-value") {
				t.Fatal("private error detail")
			}
		})
	}
}

func TestValidationOverlayMatchesPinnedLayerSemantics(t *testing.T) {
	base, err := validationDocument([]byte("model='base'\n[model_providers.custom]\nname='Custom'\nbase_url='https://example.com/v1'\n[memories]\ndisable_on_external_context=false\n[shell_environment_policy]\ninclude_only=['A','B']"))
	if err != nil {
		t.Fatal(err)
	}
	profile, err := validationDocument([]byte("model='profile'\n[model_providers.custom]\nname='Override'\n[memories]\nno_memories_if_mcp_or_web_search=true\n[shell_environment_policy]\ninclude_only=['C']"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := validationDocument([]byte("model='profile'\n[model_providers.custom]\nname='Override'\nbase_url='https://example.com/v1'\n[memories]\ndisable_on_external_context=true\n[shell_environment_policy]\ninclude_only=['C']"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(overlayConfig(base, profile), want) {
		t.Fatal("profile did not replace scalar/array values and recursively overlay tables")
	}
	if base["model"] != "base" || base["memories"].(map[string]any)["disable_on_external_context"] != false || base["model_providers"].(map[string]any)["custom"].(map[string]any)["name"] != "Custom" {
		t.Fatal("overlay modified the base used by other profiles")
	}
}

func TestValidateConfigLimitsAndCancellation(t *testing.T) {
	for _, version := range []string{"", "0.144.5", "0.144.7", "0.145.0", "0.144.6-beta", "codex-cli 0.144.6", "0.144.6\n"} {
		if err := ValidateConfigSet(t.Context(), version, nil, nil); !errors.Is(err, ErrUnsupportedVersion) {
			t.Fatal("unverified version accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ValidateConfigSet(ctx, SupportedConfigVersion, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := ValidateConfigSet(t.Context(), SupportedConfigVersion, bytes.Repeat([]byte("#"), MaxBytes+1), nil); !errors.Is(err, ErrSize) {
		t.Fatal(err)
	}
	if err := ValidateConfigSet(t.Context(), SupportedConfigVersion, nil, make([][]byte, 33)); !errors.Is(err, ErrSize) {
		t.Fatal(err)
	}
	profiles := make([][]byte, 9)
	for i := range profiles {
		profiles[i] = bytes.Repeat([]byte("#"), MaxBytes)
	}
	if err := ValidateConfigSet(t.Context(), SupportedConfigVersion, nil, profiles); !errors.Is(err, ErrSize) {
		t.Fatal(err)
	}
	deep := []byte("x=" + strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65))
	if err := ValidateConfigSet(t.Context(), SupportedConfigVersion, deep, nil); !errors.Is(err, ErrDepth) {
		t.Fatal(err)
	}
}

func TestValidationConcurrentAndDoesNotModifyInputs(t *testing.T) {
	for i := range 8 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			base := []byte("model='base'\n[history]\npersistence='save-all'")
			profile := []byte("model='profile'")
			original := append([]byte(nil), base...)
			if err := ValidateConfigSet(t.Context(), SupportedConfigVersion, base, [][]byte{profile}); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(base, original) || string(profile) != "model='profile'" {
				t.Fatal("input modified")
			}
		})
	}
}
