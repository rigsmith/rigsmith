package configcodec

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"math"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// SupportedConfigVersion is an exact, audited release, not a minimum version.
const SupportedConfigVersion = "0.144.6"

var (
	ErrUnsupportedVersion = errors.New("unsupported Codex configuration version")
	ErrValidation         = errors.New("Codex configuration does not satisfy the pinned validation policy")
)

//go:embed schema/codex-0.144.6.json
var configSchema []byte

var compiledConfigSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	// Only bundled local references are allowed. No configuration, schema or
	// environment value can cause a network request during validation.
	compiler.UseLoader(jsonschema.SchemeURLLoader{})
	compiler.AssertFormat()
	for name, bounds := range map[string][2]int64{
		"int32": {math.MinInt32, math.MaxInt32}, "int64": {math.MinInt64, math.MaxInt64},
		"uint16": {0, math.MaxUint16}, "uint32": {0, math.MaxUint32},
		"uint64": {0, math.MaxInt64}, "uint": {0, math.MaxInt64},
	} {
		compiler.RegisterFormat(&jsonschema.Format{Name: name, Validate: func(v any) error {
			n, ok := v.(int64)
			if !ok || n < bounds[0] || n > bounds[1] {
				return ErrValidation
			}
			return nil
		}})
	}
	compiler.RegisterFormat(&jsonschema.Format{Name: "double", Validate: func(v any) error {
		switch n := v.(type) {
		case int64:
			return nil
		case float64:
			if !math.IsNaN(n) && !math.IsInf(n, 0) {
				return nil
			}
		}
		return ErrValidation
	}})
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(configSchema))
	if err != nil {
		return nil, ErrValidation
	}
	const resource = "urn:codexrig:config:0.144.6"
	if err := compiler.AddResource(resource, doc); err != nil {
		return nil, ErrValidation
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		return nil, ErrValidation
	}
	return schema, nil
})

// ValidateConfigSet checks one base document and each independent profile overlay
// against a pinned release schema and explicit legacy-profile/provider checks.
// A nil base represents no base file. Inputs may contain private local values;
// errors never include them. Nothing is executed, fetched, written or logged.
// This is structural validation, not a Codex startup or destination-readiness
// check. Callers must still validate managed layers, artifacts and credentials.
func ValidateConfigSet(ctx context.Context, version string, base []byte, profiles [][]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if version != SupportedConfigVersion {
		return ErrUnsupportedVersion
	}
	count := len(profiles)
	if base != nil {
		count++
	}
	if count > 32 {
		return ErrSize
	}
	total := len(base)
	for _, data := range profiles {
		if len(data) > MaxBytes {
			return ErrSize
		}
		total += len(data)
	}
	if len(base) > MaxBytes || total > 8<<20 {
		return ErrSize
	}
	schema, err := compiledConfigSchema()
	if err != nil {
		return err
	}
	current, err := validationDocument(base)
	if err != nil {
		return err
	}
	if err := validateEffective(ctx, schema, current); err != nil {
		return err
	}
	for _, data := range profiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		profile, err := validationDocument(data)
		if err != nil {
			return err
		}
		// Profiles overlay the base independently. They never inherit from another
		// profile, and table merge is distinct from the secret-preserving restore.
		effective := overlayConfig(current, profile)
		if err := validateEffective(ctx, schema, effective); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func validationDocument(data []byte) (map[string]any, error) {
	doc, err := parse(data)
	if err != nil {
		return nil, err
	}
	if err := checkDepth(doc, 0); err != nil {
		return nil, err
	}
	if !validationValue(doc) {
		return nil, ErrValidation
	}
	// The pinned loader canonicalizes this alias in each layer before merging.
	// Canonical values win within a layer; profile aliases still override base
	// canonical values. This only changes our parsed copy, never restore bytes.
	if memories, ok := doc["memories"].(map[string]any); ok {
		const legacy = "no_memories_if_mcp_or_web_search"
		const canonical = "disable_on_external_context"
		if value, exists := memories[legacy]; exists {
			if _, exists := memories[canonical]; !exists {
				memories[canonical] = value
			}
			delete(memories, legacy)
		}
	}
	return doc, nil
}

func validationValue(v any) bool {
	switch value := v.(type) {
	case map[string]any:
		for _, child := range value {
			if !validationValue(child) {
				return false
			}
		}
	case []any:
		for _, child := range value {
			if !validationValue(child) {
				return false
			}
		}
	case string, bool, int64:
	case float64:
		return !math.IsNaN(value) && !math.IsInf(value, 0)
	default:
		// Do not coerce TOML timestamps into strings or objects for JSON Schema.
		return false
	}
	return true
}

func validateEffective(ctx context.Context, schema *jsonschema.Schema, doc map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkDepth(doc, 0); err != nil {
		return err
	}
	if schema.Validate(doc) != nil {
		return ErrValidation
	}
	// These legacy fields remain in the release schema for compatibility/error
	// reporting, but no longer select the separate native profile files.
	if _, exists := doc["profile"]; exists {
		return ErrValidation
	}
	if _, exists := doc["profiles"]; exists {
		return ErrValidation
	}
	if provider, exists := doc["model_provider"]; exists {
		name, ok := provider.(string)
		if !ok {
			return ErrValidation
		}
		// Codex 0.144.6: built_in_model_providers in model-provider-info/src/lib.rs
		// at 5d1fbf26c43abc65a203928b2e31561cb039e06d. See schema/README.md.
		// Do not blanket-reject configured built-in keys: the release ignores
		// most overrides and expressly allows Bedrock AWS profile/region settings.
		switch name {
		case "openai", "amazon-bedrock", "ollama", "lmstudio":
		default:
			providers, _ := doc["model_providers"].(map[string]any)
			if _, exists := providers[name]; !exists {
				return ErrValidation
			}
		}
	}
	return ctx.Err()
}

func overlayConfig(base, profile map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(profile))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range profile {
		left, leftOK := out[key].(map[string]any)
		right, rightOK := value.(map[string]any)
		if leftOK && rightOK {
			out[key] = overlayConfig(left, right)
		} else {
			out[key] = value
		}
	}
	return out
}
