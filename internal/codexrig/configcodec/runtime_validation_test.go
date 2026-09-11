package configcodec

import (
	"errors"
	"strings"
	"testing"
)

func TestRuntimeProviderRules(t *testing.T) {
	for _, name := range []string{"openai", "ollama", "lmstudio"} {
		for _, selection := range []string{"", "model_provider='" + name + "'\n", "model_provider='custom'\n[model_providers.custom]\nname='Custom'\n"} {
			config := selection + "[model_providers." + name + "]\nname='Collision'"
			if err := ValidateConfigSet(t.Context(), SupportedConfigVersion, []byte(config), nil); !errors.Is(err, ErrValidation) {
				t.Fatal("reserved provider declaration accepted", name, err)
			}
		}
	}
	tests := []struct {
		name, config string
		valid        bool
	}{
		{"missing custom name", "[model_providers.custom]", false},
		{"blank custom name", "[model_providers.custom]\nname='  '", false},
		{"custom AWS", "[model_providers.custom]\nname='Custom'\naws={region='us-east-1'}", false},
		{"unselected auth conflict", "[model_providers.custom]\nname='Custom'\nenv_key='PRIVATE_ENV'\nauth={command='helper'}", false},
		{"blank helper", "[model_providers.custom]\nname='Custom'\nauth={command='  '}", false},
		{"token helper conflict", "[model_providers.custom]\nname='Custom'\nexperimental_bearer_token='private-token'\nauth={command='helper'}", false},
		{"login helper conflict", "[model_providers.custom]\nname='Custom'\nrequires_openai_auth=true\nauth={command='helper'}", false},
		{"helper allowed without execution", "[model_providers.custom]\nname='Custom'\nrequires_openai_auth=false\nauth={command='nonexistent-private-helper'}", true},
		{"token and env native precedence", "[model_providers.custom]\nname='Custom'\nenv_key='PRIVATE_ENV'\nexperimental_bearer_token='private-token'", true},
		{"Bedrock empty", "[model_providers.amazon-bedrock]", true},
		{"Bedrock explicit defaults", "[model_providers.amazon-bedrock]\nname=''\nwire_api='responses'\nrequires_openai_auth=false\nsupports_websockets=false\naws={profile='example',region='us-east-1'}", true},
		{"Bedrock nonempty name", "[model_providers.amazon-bedrock]\nname='Override'", false},
		{"Bedrock empty option still present", "[model_providers.amazon-bedrock]\nbase_url=''", false},
		{"Bedrock zero option still present", "[model_providers.amazon-bedrock]\nrequest_max_retries=0", false},
		{"Bedrock empty headers still present", "[model_providers.amazon-bedrock]\nhttp_headers={}", false},
		{"Bedrock websocket override", "[model_providers.amazon-bedrock]\nsupports_websockets=true", false},
		{"Bedrock auth override", "[model_providers.amazon-bedrock]\nauth={command='helper'}", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertRuntimeConfig(t, tc.config, tc.valid)
		})
	}
}

func TestRuntimeMCPRules(t *testing.T) {
	for _, field := range []string{"url='https://example.com/mcp'", "bearer_token_env_var=''", "http_headers={}", "env_http_headers={}", "oauth={}", "oauth_resource=''", "auth='oauth'"} {
		t.Run("stdio rejects "+field, func(t *testing.T) {
			assertRuntimeConfig(t, "[mcp_servers.example]\ncommand='helper'\n"+field, false)
		})
	}
	for _, field := range []string{"args=[]", "env={}", "env_vars=[]", "cwd='.'"} {
		t.Run("http rejects "+field, func(t *testing.T) {
			assertRuntimeConfig(t, "[mcp_servers.example]\nurl='https://example.com/mcp'\n"+field, false)
		})
	}
	tests := []struct {
		name, fields string
		valid        bool
	}{
		{"missing transport", "enabled=true", false},
		{"disabled still parsed", "enabled=false", false},
		{"stdio valid", "command='nonexistent-private-helper'\nargs=['--example']\nenv={EXAMPLE='local'}\nenv_vars=['EXAMPLE',{name='LOCAL',source='local'},{name='REMOTE',source='remote'},{name='IMPLICIT'}]\ncwd='.'\nscopes=['read']", true},
		{"http valid", "url='https://example.com/mcp'\nhttp_headers={Example='local'}\nbearer_token_env_var='PRIVATE_ENV'\nauth='oauth'\noauth_resource='https://example.com'", true},
		{"unknown env source", "command='helper'\nenv_vars=[{name='EXAMPLE',source='private-unknown-source'}]", false},
		{"empty env source", "command='helper'\nenv_vars=[{name='EXAMPLE',source=''}]", false},
		{"negative startup", "command='helper'\nstartup_timeout_sec=-0.1", false},
		{"negative tool timeout", "command='helper'\ntool_timeout_sec=-1", false},
		{"seconds overflow", "command='helper'\nstartup_timeout_sec=18446744073709551616.0", false},
		{"tool seconds overflow", "command='helper'\ntool_timeout_sec=1e30", false},
		{"seconds below overflow", "command='helper'\nstartup_timeout_sec=18446744073709549568.0", true},
		{"long duration is not Go duration", "command='helper'\ntool_timeout_sec=1e12", true},
		{"integer seconds", "command='helper'\nstartup_timeout_sec=10\ntool_timeout_sec=9223372036854775807", true},
		{"zero and fractional seconds", "command='helper'\nstartup_timeout_sec=0\ntool_timeout_sec=0.25", true},
		{"milliseconds fallback", "command='helper'\nstartup_timeout_ms=9223372036854775807", true},
		{"seconds wins over milliseconds", "command='helper'\nstartup_timeout_sec=0.5\nstartup_timeout_ms=10", true},
		{"milliseconds cannot hide invalid seconds", "command='helper'\nstartup_timeout_sec=-1\nstartup_timeout_ms=10", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertRuntimeConfig(t, "[mcp_servers.example]\n"+tc.fields, tc.valid)
		})
	}
}

func assertRuntimeConfig(t *testing.T, config string, valid bool) {
	t.Helper()
	// These cases deliberately pass the schema: they exercise the new runtime
	// rules, so a schema type error cannot accidentally make a regression pass.
	doc, err := validationDocument([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := compiledConfigSchema()
	if err != nil || schema.Validate(doc) != nil {
		t.Fatal("fixture must satisfy the pinned structural schema", err)
	}
	err = ValidateConfigSet(t.Context(), SupportedConfigVersion, []byte(config), nil)
	if (err == nil) != valid || (!valid && !errors.Is(err, ErrValidation)) {
		t.Fatalf("valid=%v, got %v", valid, err)
	}
	if err != nil && (strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "PRIVATE_ENV")) {
		t.Fatal("private diagnostic escaped")
	}
}

func TestRuntimeRulesAcrossProfileOverlays(t *testing.T) {
	base := []byte("[mcp_servers.example]\ncommand='helper'\n[model_providers.custom]\nname='Custom'")
	for _, profile := range []string{
		"[mcp_servers.example]\nurl='https://example.com/mcp'", // overlay does not delete command
		"[model_providers.custom]\nname='  '",
		"[model_providers.openai]\nname='Collision'",
	} {
		if err := ValidateConfigSet(t.Context(), SupportedConfigVersion, base, [][]byte{[]byte(profile)}); !errors.Is(err, ErrValidation) {
			t.Fatal("invalid effective profile accepted", err)
		}
	}
	// A profile may contribute partial provider fields by inheriting the name.
	if err := ValidateConfigSet(t.Context(), SupportedConfigVersion, base, [][]byte{[]byte("[model_providers.custom]\nauth={command='helper'}")}); err != nil {
		t.Fatal(err)
	}
}
