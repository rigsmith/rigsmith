package configcodec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func headerAction(profile, fields string) string {
	return "\n[permissions." + profile + ".network.mitm.actions.edit]\n" + fields + "\n"
}

func TestPermissionHeaderDeclarations(t *testing.T) {
	cases := []struct {
		name, fields string
		valid        bool
	}{
		{"strip header", "strip_request_headers=['X-Example']", true},
		{"strip empty", "strip_request_headers=['']", false},
		{"strip whitespace", "strip_request_headers=[' x-example']", false},
		{"strip separator", "strip_request_headers=['x:example']", false},
		{"strip unicode", "strip_request_headers=['x-é']", false},
		{"env source", "inject_request_headers=[{name='X-Example',secret_env_var='EXAMPLE_SOURCE'}]", true},
		{"injected missing name", "inject_request_headers=[{secret_env_var='EXAMPLE_SOURCE'}]", false},
		{"injected invalid name", "inject_request_headers=[{name='x:example',secret_env_var='EXAMPLE_SOURCE'}]", false},
		{"missing source", "inject_request_headers=[{name='x-example'}]", false},
		{"dual source", "inject_request_headers=[{name='x-example',secret_env_var='EXAMPLE_SOURCE',secret_file='/example'}]", false},
		{"empty source present alongside file", "inject_request_headers=[{name='x-example',secret_env_var='',secret_file='/example'}]", false},
		{"empty env source", "inject_request_headers=[{name='x-example',secret_env_var=''}]", false},
		{"whitespace env source", "inject_request_headers=[{name='x-example',secret_env_var='  '} ]", false},
		{"empty file source", "inject_request_headers=[{name='x-example',secret_file=''}]", false},
		{"relative file source", "inject_request_headers=[{name='x-example',secret_file='example/source'}]", false},
		{"home expansion not accepted", "inject_request_headers=[{name='x-example',secret_file='~/example'}]", false},
	}
	schema, err := compiledConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := []byte("default_permissions='work'" + headerAction("work", tc.fields) + networkHook("work", "request", "['edit']"))
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
				t.Fatal("unexpected header validation", err)
			}
			if !bytes.Equal(input, original) {
				t.Fatal("validation changed source")
			}
		})
	}
}

func TestPermissionHeaderNameGrammar(t *testing.T) {
	for _, name := range []string{"X-Test", "a!#$%&'*+-.^_`|~09", strings.Repeat("a", 65535)} {
		if !validNetworkHeaderName(name) {
			t.Fatal("valid token refused")
		}
	}
	for _, name := range []string{"", "x y", "x\ty", "x\ny", "x\ry", "x:y", "x/y", "x\\y", "x(y)", "x[y]", "x=y", "x@y", "é", "x\x7f", "x\x00", strings.Repeat("a", 65536)} {
		if validNetworkHeaderName(name) {
			t.Fatal("invalid token accepted")
		}
	}
}

func TestPermissionHeaderFileSourcesUseDestinationSyntax(t *testing.T) {
	// A missing file remains syntactically valid: validation must not read it.
	missing := filepath.Join(t.TempDir(), "absent", "source")
	for _, tc := range []struct {
		name, path string
		valid      bool
	}{
		{"missing absolute", missing, true},
		{"dot segments", filepath.Dir(missing) + string(filepath.Separator) + "../source", true},
		{"unix absolute", "/example/source", runtime.GOOS != "windows"},
		{"windows drive", `C:\example\source`, runtime.GOOS == "windows"},
		{"windows UNC", `\\server\share\source`, runtime.GOOS == "windows"},
		{"drive relative", `C:source`, false},
		{"rooted without drive", `\source`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, _ := json.Marshal(tc.path)
			input := []byte("default_permissions='work'" + headerAction("work", "inject_request_headers=[{name='x-example',secret_file="+string(encoded)+"}]") + networkHook("work", "request", "['edit']"))
			err := ValidateConfigSet(t.Context(), SupportedConfigVersion, input, nil)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrValidation) {
				t.Fatal("unexpected path validation", err)
			}
		})
	}
	action := map[string]any{"inject_request_headers": []any{map[string]any{"name": "x-example", "secret_file": missing + "\x00"}}}
	if err := validateNetworkActionHeaders(t.Context(), action); !errors.Is(err, ErrValidation) {
		t.Fatal("NUL source accepted", err)
	}
}

func TestPermissionHeaderInheritanceAndActivation(t *testing.T) {
	prefix := "default_permissions='child'\n[permissions.child]\nextends='parent'\n"
	invalid := headerAction("parent", "inject_request_headers=[{name='x-example'}]")
	validChild := headerAction("child", "strip_request_headers=['x-example']")
	hook := networkHook("child", "request", "['edit']")
	cases := []struct {
		name, config string
		valid        bool
	}{
		{"inherited invalid source", prefix + invalid + hook, false},
		{"child operation defaults replace parent", prefix + invalid + validChild + hook, true},
		{"child injection resets parent strip", prefix + headerAction("parent", "strip_request_headers=['bad:name']") + headerAction("child", "inject_request_headers=[{name='x-example',secret_env_var='EXAMPLE_SOURCE'}]") + hook, true},
		{"unused action not compiled", "default_permissions='parent'" + invalid, true},
		{"inactive action not compiled", "default_permissions=':read-only'" + invalid + networkHook("parent", "request", "['edit']"), true},
		{"unreferenced action in selected profile", "default_permissions='parent'" + invalid + networkAction("parent", "strip") + networkHook("parent", "request", "['strip']"), true},
		{"header match invalid name", "default_permissions='child'" + validChild + hook + "[permissions.child.network.mitm.hooks.request.headers]\n'bad:name'=['value']", false},
		{"header match empty values allowed", "default_permissions='child'" + validChild + hook + "[permissions.child.network.mitm.hooks.request.headers]\n'X-Example'=[]", true},
		{"empty child header map retains invalid parent", prefix + headerAction("parent", "strip_request_headers=['x-example']") + networkHook("parent", "request", "['edit']") + "[permissions.parent.network.mitm.hooks.request.headers]\n'bad:name'=['value']\n" + hook + "[permissions.child.network.mitm.hooks.request.headers]\n", false},
		{"disabled selected network still validated", "default_permissions='parent'\n[permissions.parent.network]\nenabled=false\n" + invalid + networkHook("parent", "request", "['edit']"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfigSet(t.Context(), SupportedConfigVersion, []byte(tc.config), nil)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrValidation) {
				t.Fatal("unexpected inheritance validation", err)
			}
		})
	}
}

func TestPermissionHeaderLayerAndManagedValidation(t *testing.T) {
	bad := headerAction("work", "inject_request_headers=[{name='x-example'}]")
	good := headerAction("work", "strip_request_headers=['x-example']")
	hook := networkHook("work", "request", "['edit']")
	cases := []struct {
		name   string
		base   []byte
		layers ValidationLayers
		valid  bool
	}{
		{"raw layer retains omitted operation", []byte("default_permissions='work'" + good + hook), ValidationLayers{Before: [][]byte{[]byte(bad)}}, false},
		{"raw layer explicitly clears operation", []byte("default_permissions='work'" + good + "inject_request_headers=[]\n" + hook), ValidationLayers{Before: [][]byte{[]byte(bad)}}, true},
		{"managed fallback invalid source", nil, ValidationLayers{Requirements: []byte("default_permissions='work'\n[allowed_permission_profiles]\nwork=true\n" + bad + hook)}, false},
		{"legacy selection skips header compilation", []byte("default_permissions='work'" + bad + hook), ValidationLayers{After: [][]byte{[]byte("sandbox_mode='read-only'")}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfigSetWithLayers(t.Context(), SupportedConfigVersion, tc.base, nil, tc.layers)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrValidation) {
				t.Fatal("unexpected layer validation", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := validateNetworkActionHeaders(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
