package configcodec

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func matcherHook(fields map[string]any) map[string]any {
	hook := map[string]any{"host": "example.test", "methods": []string{"GET"}, "path_prefixes": []string{"/"}, "action": []string{"strip"}}
	for k, v := range fields {
		hook[k] = v
	}
	return hook
}
func matcherProfile(hook map[string]any) map[string]any {
	return map[string]any{"network": map[string]any{"mitm": map[string]any{"actions": map[string]any{"strip": map[string]any{"strip_request_headers": []string{"x-example"}}}, "hooks": map[string]any{"request": hook}}}}
}
func matcherDocument(t *testing.T, profiles map[string]any, selected string) []byte {
	t.Helper()
	data, err := toml.Marshal(map[string]any{"default_permissions": selected, "permissions": profiles})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPermissionMatcherDeclarations(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
		valid  bool
	}{
		{"default", nil, true},
		{"empty host", map[string]any{"host": ""}, false},
		{"blank host", map[string]any{"host": " \t"}, false},
		{"trailing dots only", map[string]any{"host": "...:443"}, false},
		{"empty bracket host", map[string]any{"host": "[]:443"}, false},
		{"wildcard host", map[string]any{"host": "*.example.test"}, false},
		{"normalized host", map[string]any{"host": " EXAMPLE.TEST.:443 "}, true},
		{"scoped IPv6", map[string]any{"host": "[fe80::1%25eth0]:443"}, true},
		{"unbracketed IPv6", map[string]any{"host": "::1"}, true},
		{"port discarded before wildcard check", map[string]any{"host": "example.test:*"}, true},
		{"empty methods", map[string]any{"methods": []string{}}, false},
		{"blank method", map[string]any{"methods": []string{"GET", "  "}}, false},
		{"method trim", map[string]any{"methods": []string{" get "}}, true},
		{"native method check is nonblank only", map[string]any{"methods": []string{"CUSTOM METHOD"}}, true},
		{"empty paths", map[string]any{"path_prefixes": []string{}}, false},
		{"empty literal path", map[string]any{"path_prefixes": []string{"literal:"}}, false},
		{"unprefixed empty path", map[string]any{"path_prefixes": []string{""}}, false},
		{"blank literal path allowed", map[string]any{"path_prefixes": []string{" "}}, true},
		{"empty glob path", map[string]any{"path_prefixes": []string{"pattern:"}}, false},
		{"literal metacharacters", map[string]any{"path_prefixes": []string{"literal:pattern:["}}, true},
		{"unprefixed metacharacters literal", map[string]any{"path_prefixes": []string{"["}}, true},
		{"path glob", map[string]any{"path_prefixes": []string{"pattern:/{api,v1}/**/[a-z]?"}}, true},
		{"malformed path glob", map[string]any{"path_prefixes": []string{"pattern:/[z-a]"}}, false},
		{"body rejected", map[string]any{"body": map[string]any{}}, false},
		{"false body still present", map[string]any{"body": false}, false},
		{"empty query name", map[string]any{"query": map[string]any{"": []string{"v"}}}, false},
		{"empty query alternatives", map[string]any{"query": map[string]any{"q": []string{}}}, false},
		{"empty query literal", map[string]any{"query": map[string]any{"q": []string{"", "literal:"}}}, true},
		{"blank query name permitted", map[string]any{"query": map[string]any{" ": []string{"value"}}}, true},
		{"bad query pattern", map[string]any{"query": map[string]any{"q": []string{"pattern:{a,b"}}}, false},
		{"empty header values permitted", map[string]any{"headers": map[string]any{"x-example": []string{}}}, true},
		{"empty header literal", map[string]any{"headers": map[string]any{"x-example": []string{""}}}, true},
		{"bad header glob", map[string]any{"headers": map[string]any{"x-example": []string{"pattern:a\\"}}}, false},
	}
	schema, err := compiledConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := matcherDocument(t, map[string]any{"work": matcherProfile(matcherHook(tc.fields))}, "work")
			original := bytes.Clone(input)
			doc, err := validationDocument(input)
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(doc); err != nil {
				t.Fatal("fixture must pass schema", err)
			}
			err = ValidateConfigSet(t.Context(), SupportedConfigVersion, input, nil)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrValidation) {
				t.Fatal("unexpected matcher validation", err)
			}
			if !bytes.Equal(input, original) {
				t.Fatal("validation changed input")
			}
		})
	}
}

func TestNetworkGlobSyntax(t *testing.T) {
	valid := []string{"*", "**", "a**b", "?", "{a,{b,c}}", "{}", "{,}", "a,b", `\{a\}`, "[a-z]", "[]]", "[-a]", "[a-]", "[!a-z]", "[^a]", "[--z]", "[]-z]", `[\]`, "[é-ü]", strings.Repeat("{", 1024) + "a" + strings.Repeat("}", 1024)}
	invalid := []string{"[", "[]", "[!", "[!]", "[z-a]", "[z--]", "{a,b", "{a,{b,c}", "a,b}", "{a,b}}", `a\`}
	for _, pattern := range valid {
		if err := validateNetworkGlobSyntax(t.Context(), pattern); err != nil {
			t.Errorf("valid glob refused %q: %v", pattern, err)
		}
	}
	for _, pattern := range invalid {
		if err := validateNetworkGlobSyntax(t.Context(), pattern); !errors.Is(err, ErrValidation) {
			t.Errorf("invalid glob accepted %q: %v", pattern, err)
		}
	}
	for _, pattern := range []string{`**\`, `a/**\`, `{**,**\}`} {
		err := validateNetworkGlobSyntax(t.Context(), pattern)
		wantValid := runtime.GOOS == "windows"
		// Last fixture has a closing brace escaped on Unix, but consumed separator on Windows.
		if wantValid && err != nil || !wantValid && !errors.Is(err, ErrValidation) {
			t.Fatal("platform recursive-star escape", pattern, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := validateNetworkGlobSyntax(ctx, "[a-z]"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPermissionMatcherInheritance(t *testing.T) {
	for _, tc := range []struct {
		name          string
		parent, child map[string]any
		valid         bool
	}{
		{"replace invalid parent path", map[string]any{"path_prefixes": []string{"pattern:["}}, nil, true},
		{"retain parent query", map[string]any{"query": map[string]any{"q": []string{"pattern:["}}}, nil, false},
		{"replace matching query", map[string]any{"query": map[string]any{"q": []string{"pattern:["}}}, map[string]any{"query": map[string]any{"q": []string{"literal:["}}}, true},
		{"empty child query retains parent", map[string]any{"query": map[string]any{"q": []string{}}}, map[string]any{"query": map[string]any{}}, false},
		{"different child query retains parent", map[string]any{"query": map[string]any{"q": []string{}}}, map[string]any{"query": map[string]any{"other": []string{"ok"}}}, false},
		{"body inherited", map[string]any{"body": true}, nil, false},
		{"header matcher inherited", map[string]any{"headers": map[string]any{"x-example": []string{"pattern:["}}}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := matcherProfile(matcherHook(tc.parent))
			child := matcherProfile(matcherHook(tc.child))
			child["extends"] = "parent"
			input := matcherDocument(t, map[string]any{"parent": parent, "child": child}, "child")
			err := ValidateConfigSet(t.Context(), SupportedConfigVersion, input, nil)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrValidation) {
				t.Fatal("unexpected inherited matcher validation", err)
			}
			inactive := matcherDocument(t, map[string]any{"parent": parent, "child": child}, ":read-only")
			if err := ValidateConfigSet(t.Context(), SupportedConfigVersion, inactive, nil); err != nil {
				t.Fatal("inactive matcher compiled", err)
			}
		})
	}
}
