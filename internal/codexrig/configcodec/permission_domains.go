package configcodec

import (
	"context"
	"net/netip"
	"runtime"
	"sort"
	"strings"
)

// Resolve only selected domain declarations, after schema validation. Native
// inheritance normalizes both domain maps whenever both are present, including
// an empty map. BTreeMap order determines the winner within one declaration;
// the child then replaces equivalent ancestor keys. Keep inputs immutable.
func validateInheritedNetworkDomains(ctx context.Context, chain []map[string]any) error {
	var domains map[string]string
	for i := len(chain) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := profileMap(profileMap(chain[i]["network"])["domains"])
		if child == nil {
			continue
		}
		next := make(map[string]string, len(child))
		for key, value := range child {
			next[key] = value.(string)
		}
		if domains == nil {
			domains = next
			continue
		}
		var err error
		domains, err = normalizeNetworkDomainMap(ctx, domains)
		if err != nil {
			return err
		}
		next, err = normalizeNetworkDomainMap(ctx, next)
		if err != nil {
			return err
		}
		for key, value := range next {
			domains[key] = value
		}
	}
	// Applying the resolved profile upserts in sorted raw-key order, replacing
	// equivalent normalized keys while retaining the winning raw pattern.
	type entry struct{ pattern, permission string }
	effective := make(map[string]entry, len(domains))
	for _, pattern := range sortedNetworkDomainKeys(domains) {
		if err := ctx.Err(); err != nil {
			return err
		}
		effective[normalizeNetworkDomainHost(pattern)] = entry{pattern, domains[pattern]}
	}
	for _, entry := range effective {
		for _, candidate := range expandedNetworkDomainPatterns(entry.pattern) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.permission == "deny" && candidate == "*" {
				return ErrValidation
			}
			if err := validateNetworkGlobSyntaxWithEscape(ctx, candidate, runtime.GOOS != "windows"); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func sortedNetworkDomainKeys(domains map[string]string) []string {
	keys := make([]string, 0, len(domains))
	for key := range domains {
		keys = append(keys, key)
	}
	sort.Strings(keys) // Rust's UTF-8 BTreeMap string order.
	return keys
}

func normalizeNetworkDomainMap(ctx context.Context, domains map[string]string) (map[string]string, error) {
	normalized := make(map[string]string, len(domains))
	for _, key := range sortedNetworkDomainKeys(domains) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		normalized[normalizeNetworkDomainHost(key)] = domains[key]
	}
	return normalized, nil
}

// Pinned policy::normalize_host: permissive host fragments, not DNS/URL parsing.
// Preserve native scoped-IP spelling; only a valid address enables scope cleanup.
func normalizeNetworkDomainHost(host string) string {
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "[") && strings.Contains(host, "]") {
		host = host[1:strings.IndexByte(host, ']')]
	} else if strings.Count(host, ":") == 1 {
		host = host[:strings.IndexByte(host, ':')]
	}
	lower := []byte(host)
	for i, c := range lower {
		if c >= 'A' && c <= 'Z' {
			lower[i] += 'a' - 'A'
		}
	}
	host = strings.TrimRight(string(lower), ".")
	for _, delimiter := range []string{"%25", "%"} {
		if ip, scope, ok := strings.Cut(host, delimiter); ok && !strings.Contains(ip, "%") {
			if _, err := netip.ParseAddr(ip); err == nil {
				return ip + "%" + scope
			}
		}
	}
	return host
}

// Native normalize_pattern followed by DomainPattern::parse/expansion. A bare
// empty pattern is valid syntax. **. also includes the apex; *. excludes it.
func expandedNetworkDomainPatterns(pattern string) []string {
	pattern = strings.TrimSpace(pattern)
	prefix := ""
	for _, candidate := range []string{"**.", "*."} {
		if strings.HasPrefix(pattern, candidate) {
			prefix, pattern = candidate, strings.TrimPrefix(pattern, candidate)
			break
		}
	}
	pattern = strings.TrimSpace(prefix + normalizeNetworkDomainHost(pattern))
	if domain, ok := strings.CutPrefix(pattern, "**."); ok {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			return []string{""}
		}
		return []string{domain, "?*." + domain}
	}
	if domain, ok := strings.CutPrefix(pattern, "*."); ok {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			return []string{""}
		}
		return []string{"?*." + domain}
	}
	return []string{pattern}
}
