package configcodec

import (
	"context"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The upstream schema loses the value types of these serde-flatten maps.
// Restore them from permissions_toml.rs in the same pinned release. Fail if a
// future snapshot already defines one, forcing a new audit rather than overwrite.
func addPermissionMapSchemas(schema any) error {
	root, ok := schema.(map[string]any)
	if !ok {
		return ErrValidation
	}
	definitions, _ := root["definitions"].(map[string]any)
	for name, target := range map[string]string{
		"PermissionsToml":                  "PermissionProfileToml",
		"FilesystemPermissionsToml":        "FilesystemPermissionToml",
		"NetworkDomainPermissionsToml":     "NetworkDomainPermissionToml",
		"NetworkUnixSocketPermissionsToml": "NetworkUnixSocketPermissionToml",
		"WorkspaceRootsToml":               "boolean",
	} {
		definition, ok := definitions[name].(map[string]any)
		if !ok || definition["type"] != "object" || hasConfigKey(definition, "additionalProperties") {
			return ErrValidation
		}
		if target == "boolean" {
			definition["additionalProperties"] = map[string]any{"type": "boolean"}
		} else {
			if _, exists := definitions[target]; !exists {
				return ErrValidation
			}
			definition["additionalProperties"] = map[string]any{"$ref": "#/definitions/" + target}
		}
	}
	return nil
}

type permissionMode uint8

const (
	permissionUnspecified permissionMode = iota
	permissionLegacy
	permissionProfiles
)

func permissionLayerMode(prior permissionMode, doc map[string]any) permissionMode {
	if hasConfigKey(doc, "sandbox_mode") {
		prior = permissionLegacy
	}
	if hasConfigKey(doc, "default_permissions") {
		prior = permissionProfiles
	}
	return prior
}

type managedPermissions struct {
	profiles       map[string]any
	allowed        map[string]any // nil means unconstrained, empty means none allowed
	defaultProfile string
	hasDefault     bool
}

// Requirements are a distinct policy source. Other fields remain the mandatory
// destination validator's responsibility; never merge this document into config.
func permissionRequirements(data []byte, schema *jsonschema.Schema) (managedPermissions, error) {
	var out managedPermissions
	doc, err := validationDocument(data)
	if err != nil {
		return out, err
	}
	if value, exists := doc["default_permissions"]; exists {
		var ok bool
		out.defaultProfile, ok = value.(string)
		if !ok {
			return out, ErrValidation
		}
		out.hasDefault = true
	}
	if value, exists := doc["allowed_permission_profiles"]; exists {
		var ok bool
		out.allowed, ok = value.(map[string]any)
		if !ok {
			return out, ErrValidation
		}
		for _, value := range out.allowed {
			if _, ok := value.(bool); !ok {
				return out, ErrValidation
			}
		}
	}
	if value, exists := doc["permissions"]; exists {
		profiles, ok := value.(map[string]any)
		if !ok {
			return out, ErrValidation
		}
		out.profiles = make(map[string]any, len(profiles))
		for name, profile := range profiles {
			if name == "filesystem" {
				// This reserved entry is a requirements constraint, never a profile.
				// Its path semantics are left to full destination enforcement.
				constraint, ok := profile.(map[string]any)
				if !ok {
					return out, ErrValidation
				}
				for key := range constraint {
					if key != "deny_read" {
						return out, ErrValidation
					}
				}
				if value, exists := constraint["deny_read"]; exists {
					paths, ok := value.([]any)
					if !ok {
						return out, ErrValidation
					}
					for _, path := range paths {
						if _, ok := path.(string); !ok {
							return out, ErrValidation
						}
					}
				}
				continue
			}
			out.profiles[name] = profile
		}
		if schema.Validate(map[string]any{"permissions": out.profiles}) != nil {
			return out, ErrValidation
		}
	}
	return out, nil
}

func validatePermissionSelection(ctx context.Context, doc map[string]any, mode permissionMode, managed managedPermissions) error {
	user, _ := doc["permissions"].(map[string]any)
	catalog := make(map[string]any, len(user)+len(managed.profiles))
	for name, profile := range user {
		catalog[name] = profile
	}
	for name, profile := range managed.profiles {
		if _, exists := catalog[name]; exists {
			return ErrValidation
		}
		catalog[name] = profile
	}
	for name := range catalog {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(name, ":") {
			return ErrValidation
		}
		// Native deserialization checks definitions even in inactive profiles.
		if err := validateMITMDefinitions(ctx, permissionMITM(profileMap(catalog[name]))); err != nil {
			return err
		}
	}
	selected, hasSelection := doc["default_permissions"].(string)
	if managed.allowed == nil {
		if managed.hasDefault {
			return ErrValidation
		}
	} else {
		for name := range managed.allowed {
			// Native validation checks even disallowed (false) catalog entries.
			if !isBuiltinPermission(name) && !hasConfigKey(catalog, name) {
				return ErrValidation
			}
		}
		fallback := managed.defaultProfile
		if !managed.hasDefault {
			if managed.allowed[":workspace"] != true || managed.allowed[":read-only"] != true {
				return ErrValidation
			}
			fallback = ":workspace"
		}
		if managed.allowed[fallback] != true {
			return ErrValidation
		}
		if !hasSelection || managed.allowed[selected] != true {
			selected, hasSelection = fallback, true
		}
	}
	if !hasSelection && len(catalog) != 0 && mode != permissionLegacy && managed.allowed == nil {
		return ErrValidation
	}
	if mode == permissionLegacy && managed.allowed == nil {
		return ctx.Err() // Later legacy layer makes the retained selection inactive.
	}
	if !hasSelection || isBuiltinPermission(selected) {
		return ctx.Err()
	}
	// Only active inheritance is resolved. Native catalogs may retain inactive
	// profiles that cannot compile, marking them unavailable instead of aborting.
	seen := make(map[string]bool)
	var chain []map[string]any // selected child first, then its ancestors
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if seen[selected] {
			return ErrValidation
		}
		seen[selected] = true
		profile, ok := catalog[selected].(map[string]any)
		if !ok {
			return ErrValidation
		}
		chain = append(chain, profile)
		parent, exists := profile["extends"].(string)
		if !exists || parent == ":read-only" || parent == ":workspace" {
			// Both extensible built-ins have no explicit network declarations.
			if err := validateInheritedMITMActions(ctx, chain); err != nil {
				return err
			}
			return validateInheritedNetworkDomains(ctx, chain)
		}
		// :danger-full-access is selectable but not an extensible parent.
		selected = parent
	}
}

func isBuiltinPermission(name string) bool {
	return name == ":read-only" || name == ":workspace" || name == ":danger-full-access"
}
