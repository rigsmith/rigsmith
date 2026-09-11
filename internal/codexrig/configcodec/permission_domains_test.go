package configcodec

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"
)

func domainProfile(domains map[string]any) map[string]any {
	return map[string]any{"network": map[string]any{"domains": domains}}
}

func TestPermissionDomainDeclarations(t *testing.T) {
	cases := []struct {
		name, pattern, permission string
		valid                     bool
	}{
		{"exact", "example.test", "allow", true},
		{"apex and subdomains", "**.example.test", "deny", true},
		{"subdomains", "*.example.test", "deny", true},
		{"global allow", "*", "allow", true},
		{"global deny", "*", "deny", false},
		{"normalized global deny", " *:443 ", "deny", false},
		{"expanded global deny", "**.*", "deny", false},
		{"subdomain star permitted natively", "*.*", "deny", true},
		{"empty allowed natively", "", "deny", true},
		{"empty wildcard suffix", "**.", "deny", true},
		{"metacharacters are globs", "example.[z-a]", "allow", false},
		{"unclosed class", "example.[", "deny", false},
		{"unclosed alternate", "{api,www.example.test", "allow", false},
		{"mid label wildcard", "api-*.example.test", "allow", true},
		{"alternate", "{api,www}.example.test", "deny", true},
		{"dangling escape", `example.test\`, "allow", runtime.GOOS == "windows"},
		{"escape behavior", `example.\[`, "allow", runtime.GOOS != "windows"},
		{"bracket suffix discarded natively", "[example.test]evil*", "deny", true},
		{"bracketed global deny", "[*]ignored", "deny", false},
		{"literal prefix is not special here", "literal:[", "allow", true}, // native strips :port before glob parsing
	}
	schema, err := compiledConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := matcherDocument(t, map[string]any{"work": domainProfile(map[string]any{tc.pattern: tc.permission})}, "work")
			original := bytes.Clone(input)
			doc, err := validationDocument(input)
			if err != nil {
				t.Fatal(err)
			}
			if err = schema.Validate(doc); err != nil {
				t.Fatal("fixture must pass schema", err)
			}
			err = ValidateConfigSet(t.Context(), SupportedConfigVersion, input, nil)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrValidation) {
				t.Fatal("unexpected domain validation", err)
			}
			if !bytes.Equal(input, original) {
				t.Fatal("input changed")
			}
		})
	}
}

func TestPermissionDomainInheritance(t *testing.T) {
	cases := []struct {
		name          string
		parent, child map[string]any
		valid         bool
	}{
		{"child repairs deny", map[string]any{"*": "deny"}, map[string]any{" *.:443 ": "allow"}, true},
		{"child introduces deny", map[string]any{"*": "allow"}, map[string]any{"*": "deny"}, false},
		{"empty map retains parent", map[string]any{"*": "deny"}, map[string]any{}, false},
		{"absent map retains parent", map[string]any{"*": "deny"}, nil, false},
		{"different key retains parent", map[string]any{"*": "deny"}, map[string]any{"example.test": "allow"}, false},
		{"same declaration sorted collision", nil, map[string]any{"*": "deny", "*:443": "allow"}, true},
		{"sorted collision reverse permission", nil, map[string]any{"*": "allow", "*:443": "deny"}, false},
		{"inherited normalized collision", map[string]any{" * ": "deny", "*.": "allow"}, map[string]any{}, true},
		// Normalization is deliberately repeated only at native map-merge boundaries.
		{"single raw map retains normalized bracket", nil, map[string]any{"[*:443]": "deny"}, true},
		{"empty map triggers native normalization", map[string]any{"[*:443]": "deny"}, map[string]any{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := map[string]any{}
			if tc.parent != nil {
				parent = domainProfile(tc.parent)
			}
			child := map[string]any{"extends": "parent"}
			if tc.child != nil {
				child["network"] = map[string]any{"domains": tc.child}
			}
			profiles := map[string]any{"parent": parent, "child": child}
			err := ValidateConfigSet(t.Context(), SupportedConfigVersion, matcherDocument(t, profiles, "child"), nil)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrValidation) {
				t.Fatal("unexpected domain inheritance", err)
			}
			if err := ValidateConfigSet(t.Context(), SupportedConfigVersion, matcherDocument(t, profiles, ":read-only"), nil); err != nil {
				t.Fatal("inactive domains compiled", err)
			}
		})
	}
}

func TestNetworkDomainNormalization(t *testing.T) {
	cases := map[string]string{
		" EXAMPLE.TEST.:443 ": "example.test", "[EXAMPLE.TEST]suffix*": "example.test",
		"[fe80::1%25ETH0]:443": "fe80::1%eth0", "[fe80::1%25]": "fe80::1%",
		"[not-ip%25SCOPE]": "not-ip%25scope", "[127.0.0.1%25LOCAL]": "127.0.0.1%local",
		"[127.00.0.1%25LOCAL]": "127.00.0.1%25local", "Ä.EXAMPLE": "Ä.example", "::1": "::1",
	}
	for input, want := range cases {
		if got := normalizeNetworkDomainHost(input); got != want {
			t.Errorf("%q: got %q want %q", input, got, want)
		}
	}
	for input, want := range map[string][]string{
		" **.EXAMPLE.TEST.:443 ": {"example.test", "?*.example.test"}, "*.EXAMPLE.TEST": {"?*.example.test"}, "**.": {""}, "*": {"*"}, "**.*": {"*", "?*.*"},
	} {
		if got := expandedNetworkDomainPatterns(input); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q want %q", input, got, want)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := validateInheritedNetworkDomains(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPermissionDomainLayers(t *testing.T) {
	bad := []byte("default_permissions='work'\n[permissions.work.network.domains]\n'*'='deny'")
	good := []byte("[permissions.work.network.domains]\n'*'='allow'")
	for _, tc := range []struct {
		name     string
		base     []byte
		profiles [][]byte
		layers   ValidationLayers
		valid    bool
	}{
		{"later layer repair", bad, nil, ValidationLayers{After: [][]byte{good}}, true},
		{"later legacy deactivates", bad, nil, ValidationLayers{After: [][]byte{[]byte("sandbox_mode='read-only'")}}, true},
		{"profiles independent", []byte("default_permissions='work'\n[permissions.work]"), [][]byte{good, []byte("[permissions.work.network.domains]\n'*'='deny'")}, ValidationLayers{}, false},
		{"managed fallback checked", nil, nil, ValidationLayers{Requirements: append(bytes.Clone(bad), []byte("\n[allowed_permission_profiles]\nwork=true")...)}, false},
		{"disabled selected checked", append(bytes.Clone(bad), []byte("\n[permissions.work.network]\nenabled=false")...), nil, ValidationLayers{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfigSetWithLayers(t.Context(), SupportedConfigVersion, tc.base, tc.profiles, tc.layers)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrValidation) {
				t.Fatal("unexpected layer validation", err)
			}
		})
	}
}
