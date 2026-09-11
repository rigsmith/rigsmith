package configcodec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPermissionMapSchema(t *testing.T) {
	for _, config := range []string{
		"[permissions]\nwork=42",
		"[permissions.work]\nunknown_field=true",
		"[permissions.work]\nextends=42",
		"[permissions.work.workspace_roots]\n':project_roots'='yes'",
		"[permissions.work.filesystem]\n':root'=true",
		"[permissions.work.filesystem]\n':root'='execute'",
		"[permissions.work.filesystem]\n':project_roots'={subdir='execute'}",
		"[permissions.work.filesystem]\nglob_scan_max_depth=0",
		"[permissions.work.network.domains]\n'example.com'='read'",
		"[permissions.work.network.unix_sockets]\n'/tmp/example.sock'=true",
		"[permissions.work.network]\nenabled='yes'",
	} {
		t.Run(config, func(t *testing.T) {
			// Built-in selection isolates schema validation from profile lookup.
			err := ValidateConfigSet(t.Context(), SupportedConfigVersion, []byte("default_permissions=':read-only'\n"+config), nil)
			if !errors.Is(err, ErrValidation) {
				t.Fatal("invalid flattened permission map accepted", err)
			}
		})
	}
	valid := []byte("default_permissions='work'\n[permissions.work]\nextends=':workspace'\n[permissions.work.workspace_roots]\n':project_roots'=true\n[permissions.work.filesystem]\n':root'='read'\n':project_roots'={subdir='write'}\nglob_scan_max_depth=4\n[permissions.work.network]\nenabled=true\n[permissions.work.network.domains]\n'example.com'='allow'\n[permissions.work.network.unix_sockets]\n'/tmp/example.sock'='deny'")
	if err := ValidateConfigSet(t.Context(), SupportedConfigVersion, valid, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPermissionSelectionAndInheritance(t *testing.T) {
	tests := []struct {
		name, config string
		valid        bool
	}{
		{"undefined selection", "default_permissions='missing'", false},
		{"unknown built-in", "default_permissions=':missing'", false},
		{"reserved declaration", "[permissions.':custom']", false},
		{"built-in declaration", "[permissions.':read-only']", false},
		{"missing selection", "[permissions.work]", false},
		{"empty catalog", "[permissions]", true},
		{"legacy with profiles", "sandbox_mode='read-only'\n[permissions.work]", true},
		{"same-layer selection wins", "sandbox_mode='read-only'\ndefault_permissions='missing'", false},
		{"direct built-in unrestricted", "default_permissions=':danger-full-access'", true},
		{"read-only parent", "default_permissions='work'\n[permissions.work]\nextends=':read-only'", true},
		{"workspace parent", "default_permissions='work'\n[permissions.work]\nextends=':workspace'", true},
		{"unsupported unrestricted parent", "default_permissions='work'\n[permissions.work]\nextends=':danger-full-access'", false},
		{"unknown parent", "default_permissions='work'\n[permissions.work]\nextends='missing'", false},
		{"self cycle", "default_permissions='work'\n[permissions.work]\nextends='work'", false},
		{"two-node cycle", "default_permissions='a'\n[permissions.a]\nextends='b'\n[permissions.b]\nextends='a'", false},
		{"inactive cycle retained", "default_permissions=':read-only'\n[permissions.a]\nextends='a'", true},
		{"empty custom ID", "default_permissions=''\n[permissions.'']", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfigSet(t.Context(), SupportedConfigVersion, []byte(tc.config), nil)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, got %v", tc.valid, err)
			}
		})
	}
}

func TestManagedPermissionCatalog(t *testing.T) {
	tests := []struct {
		name, config, requirements string
		valid                      bool
	}{
		{"managed profile", "default_permissions='managed'", "[permissions.managed]\nextends=':workspace'", true},
		{"profile name collision", "default_permissions='managed'\n[permissions.managed]", "[permissions.managed]", false},
		{"managed reserved ID", "", "[permissions.':read-only']", false},
		{"default needs allowlist", "", "default_permissions=':read-only'", false},
		{"allowlist empty", "", "[allowed_permission_profiles]", false},
		{"undefined allowlist true", "", "default_permissions='missing'\n[allowed_permission_profiles]\nmissing=true", false},
		{"undefined allowlist false", "", "default_permissions=':read-only'\n[allowed_permission_profiles]\n':read-only'=true\nmissing=false", false},
		{"implicit default", "", "[allowed_permission_profiles]\n':read-only'=true\n':workspace'=true", true},
		{"single built-in needs default", "", "[allowed_permission_profiles]\n':read-only'=true", false},
		{"disallowed fallback", "", "default_permissions=':read-only'\n[allowed_permission_profiles]\n':read-only'=false", false},
		{"fallback overrides undefined user choice", "default_permissions='unknown'", "default_permissions=':read-only'\n[allowed_permission_profiles]\n':read-only'=true", true},
		{"managed force beats legacy", "sandbox_mode='read-only'", "default_permissions='managed'\n[allowed_permission_profiles]\nmanaged=true\n[permissions.managed]\nextends='missing'", false},
		{"managed inheritance", "default_permissions='child'\n[permissions.child]\nextends='parent'", "[permissions.parent]\nextends=':read-only'", true},
		{"managed fallback inheritance", "", "default_permissions='managed'\n[allowed_permission_profiles]\nmanaged=true\n[permissions.managed]\nextends=':workspace'", true},
		{"managed profile wrong shape", "", "[permissions]\nmanaged=true", false},
		{"allowlist wrong shape", "", "allowed_permission_profiles=true", false},
		{"allowlist wrong value", "", "[allowed_permission_profiles]\n':read-only'='true'", false},
		{"default wrong type", "", "default_permissions=4", false},
		{"filesystem is not profile", "", "[permissions.filesystem]\nextends=':read-only'", false},
		{"filesystem constraint retained separately", "", "[permissions.filesystem]\ndeny_read=['/private/example']", true},
		{"filesystem constraint type", "", "[permissions.filesystem]\ndeny_read=[true]", false},
		{"unknown filesystem constraint", "", "[permissions.filesystem]\nfoo=true", false},
		{"unknown filesystem table", "", "[permissions.filesystem.unknown]\nvalue=1", false},
		{"filesystem constraint scalar", "", "[permissions.filesystem]\ndeny_read='example'", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfigSetWithLayers(t.Context(), SupportedConfigVersion, []byte(tc.config), nil, ValidationLayers{Requirements: []byte(tc.requirements)})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, got %v", tc.valid, err)
			}
		})
	}
}

func TestValidationLayerOrderAndIndependentProfiles(t *testing.T) {
	legacy := []byte("sandbox_mode='read-only'")
	profile := []byte("default_permissions='missing'")
	if err := ValidateConfigSet(t.Context(), SupportedConfigVersion, profile, [][]byte{legacy}); err == nil {
		t.Fatal("invalid default scenario hidden by a named profile")
	}
	if err := ValidateConfigSetWithLayers(t.Context(), SupportedConfigVersion, profile, nil, ValidationLayers{After: [][]byte{legacy}}); err != nil {
		t.Fatal("later legacy selection did not override retained profile selection", err)
	}
	if err := ValidateConfigSetWithLayers(t.Context(), SupportedConfigVersion, legacy, nil, ValidationLayers{After: [][]byte{profile}}); !errors.Is(err, ErrValidation) {
		t.Fatal("later profile selection not checked", err)
	}
	layers := ValidationLayers{Before: [][]byte{[]byte("[model_providers.custom]\nname='Custom'\n[permissions.work]\nextends=':workspace'")}}
	base := []byte("model_provider='custom'\ndefault_permissions='work'")
	if err := ValidateConfigSetWithLayers(t.Context(), SupportedConfigVersion, base, nil, layers); err != nil {
		t.Fatal(err)
	}
	profiles := [][]byte{[]byte("[permissions.other]\nextends=':workspace'"), []byte("default_permissions='other'")}
	if err := ValidateConfigSetWithLayers(t.Context(), SupportedConfigVersion, base, profiles, layers); !errors.Is(err, ErrValidation) {
		t.Fatal("profiles inherited each other", err)
	}
	if err := ValidateConfigSetWithLayers(t.Context(), SupportedConfigVersion, base, nil, ValidationLayers{Before: layers.Before, After: [][]byte{[]byte("[permissions.work]\nextends='missing'")}}); !errors.Is(err, ErrValidation) {
		t.Fatal("higher layer ignored", err)
	}
}

func TestValidationLayerLimitsPrivacyAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ValidateConfigSetWithLayers(ctx, SupportedConfigVersion, nil, nil, ValidationLayers{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, layers := range []ValidationLayers{
		{Before: make([][]byte, 17)},
		{After: make([][]byte, 16), Requirements: []byte{}},
		{Requirements: bytes.Repeat([]byte("#"), MaxBytes+1)},
		{Before: [][]byte{bytes.Repeat([]byte("#"), MaxBytes+1)}},
		{Before: func() [][]byte {
			out := make([][]byte, 9)
			for i := range out {
				out[i] = bytes.Repeat([]byte("#"), MaxBytes)
			}
			return out
		}()},
	} {
		if err := ValidateConfigSetWithLayers(t.Context(), SupportedConfigVersion, nil, nil, layers); !errors.Is(err, ErrSize) {
			t.Fatal(err)
		}
	}
	layers := ValidationLayers{Before: [][]byte{[]byte("model='private-example'")}, Requirements: []byte("[permissions.managed]\nextends=':workspace'")}
	original := bytes.Clone(layers.Before[0])
	if err := ValidateConfigSetWithLayers(t.Context(), SupportedConfigVersion, []byte("default_permissions='managed'"), nil, layers); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(layers.Before[0], original) {
		t.Fatal("context bytes changed")
	}
	encoded, err := json.Marshal(layers)
	if err != nil || string(encoded) != "{}" || strings.Contains(fmt.Sprintf("%v %+v %#v", layers, layers, &layers), "private-example") {
		t.Fatal("private context exposed")
	}
}
