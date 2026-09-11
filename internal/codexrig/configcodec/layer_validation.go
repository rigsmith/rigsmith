package configcodec

import "context"

// ValidationLayers contains caller-selected, trusted destination layers in
// ascending precedence order. Before precedes base/profile; After follows them.
// Requirements is the already-resolved requirements document, not a config
// overlay. Only its permission catalog/selection fields are checked here.
// The caller owns discovery, trust filtering, CLI overrides, other requirements
// and freshness. These private bytes must never come from the remote backup.
type ValidationLayers struct {
	Before       [][]byte
	After        [][]byte
	Requirements []byte
}

func (ValidationLayers) String() string               { return "Codex validation layers (private)" }
func (v ValidationLayers) GoString() string           { return v.String() }
func (ValidationLayers) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }

// ValidateConfigSetWithLayers validates the default configuration and every
// independent profile in the same destination context. At most 16 context
// documents (including requirements) and 32 base/profile files are accepted;
// all input together is limited to 8 MiB and each document to MaxBytes.
// It performs no reads, writes, helper execution or credential checks.
func ValidateConfigSetWithLayers(ctx context.Context, version string, base []byte, profiles [][]byte, layers ValidationLayers) error {
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
	contextCount := len(layers.Before) + len(layers.After)
	if layers.Requirements != nil {
		contextCount++
	}
	if count > 32 || contextCount > 16 {
		return ErrSize
	}
	total := 0
	for _, group := range [][][]byte{{base, layers.Requirements}, profiles, layers.Before, layers.After} {
		for _, data := range group {
			if len(data) > MaxBytes {
				return ErrSize
			}
			total += len(data)
		}
	}
	if total > 8<<20 {
		return ErrSize
	}
	schema, err := compiledConfigSchema()
	if err != nil {
		return err
	}
	requirements, err := permissionRequirements(layers.Requirements, schema)
	if err != nil {
		return err
	}
	parseLayers := func(inputs [][]byte) ([]map[string]any, error) {
		out := make([]map[string]any, 0, len(inputs))
		for _, data := range inputs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			doc, err := validationDocument(data)
			if err != nil {
				return nil, err
			}
			out = append(out, doc)
		}
		return out, nil
	}
	before, err := parseLayers(layers.Before)
	if err != nil {
		return err
	}
	after, err := parseLayers(layers.After)
	if err != nil {
		return err
	}
	baseDoc, err := validationDocument(base)
	if err != nil {
		return err
	}
	current := map[string]any{}
	mode := permissionUnspecified
	for _, doc := range append(before, baseDoc) {
		current = overlayConfig(current, doc)
		mode = permissionLayerMode(mode, doc)
	}
	validate := func(doc map[string]any, mode permissionMode) error {
		for _, layer := range after {
			doc = overlayConfig(doc, layer)
			mode = permissionLayerMode(mode, layer)
		}
		if err := validateEffective(ctx, schema, doc); err != nil {
			return err
		}
		return validatePermissionSelection(ctx, doc, mode, requirements)
	}
	if err := validate(current, mode); err != nil {
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
		if err := validate(overlayConfig(current, profile), permissionLayerMode(mode, profile)); err != nil {
			return err
		}
	}
	return ctx.Err()
}
