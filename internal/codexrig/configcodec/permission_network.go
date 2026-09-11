package configcodec

import "context"

// Shape validation runs first. These helpers only inspect private parsed maps.
func profileMap(value any) map[string]any {
	doc, _ := value.(map[string]any)
	return doc
}

func permissionMITM(profile map[string]any) map[string]any {
	return profileMap(profileMap(profile["network"])["mitm"])
}

// NetworkMitmToml validates these definitions during deserialization, before
// selection or inheritance. A child cannot repair an empty parent action, nor
// can an inherited action list supply a declaration's required hook actions.
func validateMITMDefinitions(ctx context.Context, mitm map[string]any) error {
	for _, value := range profileMap(mitm["actions"]) {
		if err := ctx.Err(); err != nil {
			return err
		}
		action := profileMap(value)
		strip, _ := action["strip_request_headers"].([]any)
		inject, _ := action["inject_request_headers"].([]any)
		if len(strip) == 0 && len(inject) == 0 {
			return ErrValidation
		}
	}
	for _, value := range profileMap(mitm["hooks"]) {
		if err := ctx.Err(); err != nil {
			return err
		}
		actions, _ := profileMap(value)["action"].([]any)
		if len(actions) == 0 {
			return ErrValidation
		}
	}
	return ctx.Err()
}

// Resolve the action/reference and header portion of the selected network policy.
// Child declarations replace matching action operations (native omitted vectors
// deserialize to empty), and each hook has a required, replacing action list.
// Domain maps and filesystem policy are not compiled here.
// Keeping each child action whole applies native default-empty operation lists.
// Fresh maps avoid mutating the input or repeatedly copying the growing catalog.
func validateInheritedMITMActions(ctx context.Context, chain []map[string]any) error {
	actions := make(map[string]map[string]any)
	hooks := make(map[string]map[string]any)
	for i := len(chain) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		mitm := permissionMITM(chain[i])
		for name, value := range profileMap(mitm["actions"]) {
			if err := ctx.Err(); err != nil {
				return err
			}
			actions[name] = profileMap(value)
		}
		for name, value := range profileMap(mitm["hooks"]) {
			if err := ctx.Err(); err != nil {
				return err
			}
			hooks[name] = mergePermissionHook(hooks[name], profileMap(value))
		}
	}
	validated := make(map[string]bool)
	for _, hook := range hooks {
		if err := validateNetworkHookMatchers(ctx, hook); err != nil {
			return err
		}
		for _, reference := range hook["action"].([]any) {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := reference.(string)
			action, exists := actions[name]
			if !exists {
				// Explicit restore policy: refuse unresolved selected references.
				// Native selected_actions otherwise silently skips missing names.
				return ErrValidation
			}
			if !validated[name] {
				if err := validateNetworkActionHeaders(ctx, action); err != nil {
					return err
				}
				validated[name] = true
			}
		}
	}
	return ctx.Err()
}
