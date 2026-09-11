package configcodec

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func endpointDocument(t *testing.T, network map[string]any) []byte {
	t.Helper()
	return matcherDocument(t, map[string]any{"work": map[string]any{"network": network}}, "work")
}

func TestPermissionEndpointDeclarations(t *testing.T) {
	cases := []struct {
		name    string
		network map[string]any
		valid   bool
	}{
		{"defaults", map[string]any{}, true},
		{"proxy empty", map[string]any{"proxy_url": ""}, false},
		{"proxy blank", map[string]any{"proxy_url": " \t\n"}, false},
		{"socks empty even when disabled", map[string]any{"enable_socks5": false, "socks_url": ""}, false},
		{"addresses", map[string]any{"proxy_url": "http://localhost:3128", "socks_url": "127.0.0.1:8081"}, true},
		{"native loose host", map[string]any{"proxy_url": "example.test:invalid-port"}, true},
		{"full parser deferred", map[string]any{"proxy_url": "http://"}, true},
		{"relative allowed socket", map[string]any{"unix_sockets": map[string]any{"relative.sock": "allow"}}, false},
		{"all sockets flag cannot bypass allowlist", map[string]any{"dangerously_allow_all_unix_sockets": true, "unix_sockets": map[string]any{"relative.sock": "allow"}}, false},
		{"denied relative socket not validated", map[string]any{"unix_sockets": map[string]any{"relative.sock": "deny"}}, true},
		{"empty allowed socket", map[string]any{"unix_sockets": map[string]any{"": "allow"}}, false},
		{"unix absolute all platforms", map[string]any{"unix_sockets": map[string]any{"/private/tmp/example.sock": "allow"}}, true},
		{"native absolute missing file", map[string]any{"unix_sockets": map[string]any{filepath.Join(t.TempDir(), "absent.sock"): "allow"}}, true},
		{"no expansion", map[string]any{"unix_sockets": map[string]any{"~/example.sock": "allow"}}, false},
		{"no trimming", map[string]any{"unix_sockets": map[string]any{" /tmp/example.sock": "allow"}}, false},
		{"dot segments accepted", map[string]any{"unix_sockets": map[string]any{"/tmp/../example.sock": "allow"}}, true},
		{"drive absolute", map[string]any{"unix_sockets": map[string]any{`C:\example.sock`: "allow"}}, runtime.GOOS == "windows"},
		{"drive relative", map[string]any{"unix_sockets": map[string]any{`C:example.sock`: "allow"}}, false},
		{"UNC absolute", map[string]any{"unix_sockets": map[string]any{`\\server\share\example.sock`: "allow"}}, runtime.GOOS == "windows"},
		{"root relative backslash", map[string]any{"unix_sockets": map[string]any{`\example.sock`: "allow"}}, false},
		{"NUL allowed path refused by restore policy", map[string]any{"unix_sockets": map[string]any{"/private/\x00example.sock": "allow"}}, false},
		{"NUL denied entry unused", map[string]any{"unix_sockets": map[string]any{"/private/\x00example.sock": "deny"}}, true},
		{"disabled selected still checked", map[string]any{"enabled": false, "proxy_url": ""}, false},
	}
	schema, err := compiledConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := endpointDocument(t, tc.network)
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
				t.Fatal("unexpected endpoint validation", err)
			}
			if !bytes.Equal(input, original) {
				t.Fatal("input changed")
			}
		})
	}
}

func TestPermissionEndpointInheritance(t *testing.T) {
	cases := []struct {
		name          string
		parent, child map[string]any
		valid         bool
	}{
		{"address omitted retains ancestor", map[string]any{"proxy_url": ""}, nil, false},
		{"address replaced", map[string]any{"proxy_url": ""}, map[string]any{"proxy_url": "localhost:3128"}, true},
		{"address blank replaces valid", map[string]any{"socks_url": "localhost:8081"}, map[string]any{"socks_url": " "}, false},
		{"socket omitted retains ancestor", map[string]any{"unix_sockets": map[string]any{"relative": "allow"}}, nil, false},
		{"socket empty map retains ancestor", map[string]any{"unix_sockets": map[string]any{"relative": "allow"}}, map[string]any{"unix_sockets": map[string]any{}}, false},
		{"same socket deny repairs ancestor", map[string]any{"unix_sockets": map[string]any{"relative": "allow"}}, map[string]any{"unix_sockets": map[string]any{"relative": "deny"}}, true},
		{"same socket allow replaces deny", map[string]any{"unix_sockets": map[string]any{"relative": "deny"}}, map[string]any{"unix_sockets": map[string]any{"relative": "allow"}}, false},
		{"socket keys not normalized", map[string]any{"unix_sockets": map[string]any{"relative": "allow"}}, map[string]any{"unix_sockets": map[string]any{"./relative": "deny"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			child := map[string]any{"extends": "parent"}
			if tc.child != nil {
				child["network"] = tc.child
			}
			profiles := map[string]any{"parent": map[string]any{"network": tc.parent}, "child": child}
			err := ValidateConfigSet(t.Context(), SupportedConfigVersion, matcherDocument(t, profiles, "child"), nil)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrValidation) {
				t.Fatal("unexpected endpoint inheritance", err)
			}
			if err := ValidateConfigSet(t.Context(), SupportedConfigVersion, matcherDocument(t, profiles, ":read-only"), nil); err != nil {
				t.Fatal("inactive endpoint compiled", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := validateInheritedNetworkEndpoints(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPermissionEndpointLayers(t *testing.T) {
	bad := []byte("default_permissions='work'\n[permissions.work.network.unix_sockets]\n'relative'='allow'")
	repair := []byte("[permissions.work.network.unix_sockets]\n'relative'='deny'")
	for _, tc := range []struct {
		name     string
		base     []byte
		profiles [][]byte
		layers   ValidationLayers
		valid    bool
	}{
		{"later layer repair", bad, nil, ValidationLayers{After: [][]byte{repair}}, true},
		{"later legacy deactivates", bad, nil, ValidationLayers{After: [][]byte{[]byte("sandbox_mode='read-only'")}}, true},
		{"profiles independent", []byte("default_permissions='work'\n[permissions.work]"), [][]byte{repair, []byte("[permissions.work.network]\nsocks_url=''")}, ValidationLayers{}, false},
		{"managed fallback checked", nil, nil, ValidationLayers{Requirements: append(bytes.Clone(bad), []byte("\n[allowed_permission_profiles]\nwork=true")...)}, false},
		{"managed ancestor repair", []byte("default_permissions='child'\n[permissions.child]\nextends='parent'\n[permissions.child.network.unix_sockets]\n'relative'='deny'"), nil, ValidationLayers{Requirements: []byte("[permissions.parent.network.unix_sockets]\n'relative'='allow'")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfigSetWithLayers(t.Context(), SupportedConfigVersion, tc.base, tc.profiles, tc.layers)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrValidation) {
				t.Fatal("unexpected endpoint layer validation", err)
			}
		})
	}
}
