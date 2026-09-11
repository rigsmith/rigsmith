package configcodec

import (
	"context"
	"runtime"
	"strings"
)

// Each merged hook owns its maps. Lists are immutable parsed input; required
// scalar/list fields replace, while default-empty query/header maps merge.
// A body declaration is retained: this release cannot compile body matchers.
func mergePermissionHook(merged, child map[string]any) map[string]any {
	if merged == nil {
		merged = make(map[string]any)
	}
	for key, value := range child {
		if key == "query" || key == "headers" {
			target := profileMap(merged[key])
			if target == nil {
				target = make(map[string]any)
				merged[key] = target
			}
			for name, values := range profileMap(value) {
				target[name] = values
			}
		} else {
			merged[key] = value
		}
	}
	return merged
}

// Only declaration validity is checked. No request matching, proxy startup,
// DNS, filesystem access or secret resolution takes place.
func validateNetworkHookMatchers(ctx context.Context, hook map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	host := strings.TrimSpace(hook["host"].(string))
	if strings.HasPrefix(host, "[") && strings.Contains(host, "]") {
		host = host[1:strings.IndexByte(host, ']')]
	} else if strings.Count(host, ":") == 1 {
		host = host[:strings.IndexByte(host, ':')]
	}
	// Native normalization also lowercases and normalizes scoped IP literals;
	// neither operation changes emptiness or wildcard presence, our only checks.
	host = strings.TrimRight(host, ".")
	if host == "" || strings.Contains(host, "*") {
		return ErrValidation
	}
	methods := hook["methods"].([]any)
	if len(methods) == 0 {
		return ErrValidation
	}
	for _, value := range methods {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.TrimSpace(value.(string)) == "" {
			return ErrValidation
		}
	}
	paths := hook["path_prefixes"].([]any)
	if len(paths) == 0 || hasConfigKey(hook, "body") {
		return ErrValidation
	}
	for _, value := range paths {
		if err := validateNetworkMatcher(ctx, value.(string), true); err != nil {
			return err
		}
	}
	for _, field := range []string{"query", "headers"} {
		for name, value := range profileMap(hook[field]) {
			if err := ctx.Err(); err != nil {
				return err
			}
			values := value.([]any)
			if field == "query" {
				if name == "" || len(values) == 0 {
					return ErrValidation
				}
			} else if !validNetworkHeaderName(name) {
				return ErrValidation
			}
			for _, value := range values {
				if err := validateNetworkMatcher(ctx, value.(string), false); err != nil {
					return err
				}
			}
		}
	}
	return ctx.Err()
}

func validateNetworkMatcher(ctx context.Context, pattern string, path bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if literal, ok := strings.CutPrefix(pattern, "literal:"); ok {
		if path && literal == "" {
			return ErrValidation
		}
		return nil
	}
	if glob, ok := strings.CutPrefix(pattern, "pattern:"); ok {
		if glob == "" {
			return ErrValidation
		}
		return validateNetworkGlobSyntax(ctx, glob)
	}
	if path && pattern == "" {
		return ErrValidation
	}
	return nil
}

// Syntax rules of pinned globset 0.4.18 with backslash_escape=true and
// allow_unclosed_class=false. Iterative and linear; stars have no syntax errors.
// This does not compile the native regex engine or certify matching semantics.
func validateNetworkGlobSyntax(ctx context.Context, pattern string) error {
	return validateNetworkGlobSyntaxWithEscape(ctx, pattern, true)
}

// Domain globs use upstream platform defaults, unlike MITM's explicit escaping.
func validateNetworkGlobSyntaxWithEscape(ctx context.Context, pattern string, backslashEscape bool) error {
	chars := []rune(pattern)
	branches := []bool{false} // whether this alternate branch has a token
	for i := 0; i < len(chars); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch chars[i] {
		case '\\':
			if backslashEscape {
				i++
				if i == len(chars) {
					return ErrValidation
				}
			}
		case '*':
			if i+1 < len(chars) && chars[i+1] == '*' {
				leading := !branches[len(branches)-1]
				boundary := i > 0 && (networkGlobSeparator(chars[i-1]) || len(branches) > 1 && (chars[i-1] == '{' || chars[i-1] == ','))
				i++
				// Native recursive stars consume separators, including a
				// Windows backslash, even with backslash escaping enabled.
				if i+1 < len(chars) && networkGlobSeparator(chars[i+1]) && (leading || boundary) {
					i++
				}
			}
		case '{':
			branches = append(branches, false)
			continue
		case '}':
			if len(branches) == 1 {
				return ErrValidation
			}
			branches = branches[:len(branches)-1]
		case ',':
			if len(branches) > 1 {
				branches[len(branches)-1] = false
				continue
			}
		case '[':
			i++
			if i < len(chars) && (chars[i] == '!' || chars[i] == '^') {
				i++
			}
			first, inRange, closed := true, false, false
			var start rune
			for ; i < len(chars); i++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				c := chars[i]
				if c == ']' && !first {
					closed = true
					break
				}
				switch {
				case c == '-' && !first && !inRange:
					inRange = true
				case inRange:
					if c < start {
						return ErrValidation
					}
					inRange = false
				default:
					start = c
				}
				first = false
			}
			if !closed {
				return ErrValidation
			}
		}
		branches[len(branches)-1] = true
	}
	if len(branches) != 1 {
		return ErrValidation
	}
	return ctx.Err()
}

func networkGlobSeparator(c rune) bool { return c == '/' || runtime.GOOS == "windows" && c == '\\' }
