package configcodec

import (
	"regexp"
	"strings"
)

// localPathField is vendor policy, not a host filesystem lookup. Omit whole
// coupled settings where keeping only their public half could change identity,
// weaken a policy, or leave a reference to an artifact we have not captured.
func localPathField(path []string, key string) bool {
	if len(path) == 0 {
		switch key {
		case "permissions", "default_permissions", "sandbox_mode", "sandbox_workspace_write",
			"model_instructions_file", "experimental_compact_prompt_file", "model_catalog_json",
			"otel", "marketplaces", "plugins":
			return true
		}
	}
	if len(path) == 1 {
		switch path[0] {
		case "skills":
			return key == "config"
		case "desktop":
			return key == "custom_file_handlers"
		case "features":
			return key == "network_proxy"
		case "shell_environment_policy":
			return key == "set"
		}
	}
	if len(path) == 2 {
		switch path[0] {
		case "agents":
			return key == "config_file"
		case "mcp_servers":
			return key == "cwd"
		}
	}
	return false
}

// These tripwires are deliberately independent of runtime.GOOS. A backup may
// move between all supported operating systems. They recognize explicit local
// references, including in prose, without guessing a meaning for every slash
// (model IDs, tool names, and repository-relative labels can also contain one).
var localReference = regexp.MustCompile(`(?i)(^|[\s"'` + "`" + `=(:,;\[<{])(?:[/\\]|[a-z]:|\.\.?[/\\]|~(?:[a-z0-9_.-]+)?(?:[/\\]|$)|file:)`)
var environmentReference = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*|\$\{[^}\r\n]+\}|%[A-Za-z_][A-Za-z0-9_]*%`)

func hasLocalReference(value string) bool {
	// A normal URL's path is remote. File URIs remain local, including opaque
	// file:relative and single-slash file:/ forms. Credential checks run separately
	// before this check, so this exemption cannot bypass the secret tripwire.
	withoutURLs := urlInText.ReplaceAllStringFunc(value, func(raw string) string {
		if strings.HasPrefix(strings.ToLower(raw), "file:") {
			return raw
		}
		return " "
	})
	return localReference.MatchString(withoutURLs) || environmentReference.MatchString(value)
}
