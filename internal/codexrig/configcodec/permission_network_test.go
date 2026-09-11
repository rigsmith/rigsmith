package configcodec

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func networkAction(profile, name string) string {
	return "\n[permissions." + profile + ".network.mitm.actions." + name + "]\nstrip_request_headers=['x-example']\n"
}

func networkHook(profile, name, actions string) string {
	return "\n[permissions." + profile + ".network.mitm.hooks." + name + "]\nhost='example.test'\nmethods=['GET']\npath_prefixes=['/']\naction=" + actions + "\n"
}

func TestPermissionNetworkActionValidation(t *testing.T) {
	cases := []struct {
		name, config string
		valid        bool
	}{
		{"named action", "default_permissions='work'" + networkAction("work", "strip") + networkHook("work", "request", "['strip']"), true},
		{"empty action", "default_permissions='work'\n[permissions.work.network.mitm.actions.empty]", false},
		{"explicit empty operations", "default_permissions='work'\n[permissions.work.network.mitm.actions.empty]\nstrip_request_headers=[]\ninject_request_headers=[]", false},
		{"unknown operation does not fill action", "default_permissions='work'\n[permissions.work.network.mitm.actions.empty]\nunknown=true", false},
		{"injection counts as operation", "default_permissions='work'\n[permissions.work.network.mitm.actions.inject]\ninject_request_headers=[{name='x-example',secret_env_var='EXAMPLE_VALUE'}]" + networkHook("work", "request", "['inject']"), true},
		{"empty hook actions", "default_permissions='work'" + networkHook("work", "request", "[]"), false},
		{"undefined selected action", "default_permissions='work'" + networkHook("work", "request", "['missing']"), false},
		{"action references are case sensitive", "default_permissions='work'" + networkAction("work", "strip") + networkHook("work", "request", "['STRIP']"), false},
		{"all references checked", "default_permissions='work'" + networkAction("work", "strip") + networkHook("work", "request", "['strip','missing']"), false},
		{"duplicates supported", "default_permissions='work'" + networkAction("work", "strip") + networkHook("work", "request", "['strip','strip']"), true},
		{"unused action allowed", "default_permissions='work'" + networkAction("work", "strip"), true},
		{"inactive empty action still refused", "default_permissions=':read-only'\n[permissions.unused.network.mitm.actions.empty]", false},
		{"inactive empty hook still refused", "default_permissions=':read-only'" + networkHook("unused", "request", "[]"), false},
		{"inactive undefined reference retained", "default_permissions=':read-only'" + networkHook("unused", "request", "['missing']"), true},
		{"same-layer selection overrides legacy", "sandbox_mode='read-only'\ndefault_permissions='unused'" + networkHook("unused", "request", "['missing']"), false},
		{"legacy definitions still checked", "sandbox_mode='read-only'\n[permissions.unused.network.mitm.actions.empty]", false},
		{"disabled selected network still checks references", "default_permissions='work'\n[permissions.work.network]\nenabled=false" + networkHook("work", "request", "['missing']"), false},
	}
	schema, err := compiledConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := validationDocument([]byte(tc.config))
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(doc); err != nil {
				t.Fatal("fixture must pass schema", err)
			}
			original := []byte(tc.config)
			input := bytes.Clone(original)
			err = ValidateConfigSet(t.Context(), SupportedConfigVersion, input, nil)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrValidation) {
				t.Fatal("unexpected validation", err)
			}
			if !bytes.Equal(input, original) {
				t.Fatal("validation changed input")
			}
		})
	}
}

func TestPermissionNetworkInheritedReferences(t *testing.T) {
	base := "default_permissions='child'\n[permissions.child]\nextends='parent'\n"
	cases := []struct {
		name, config string
		valid        bool
	}{
		{"parent action child hook", base + networkAction("parent", "strip") + networkHook("child", "request", "['strip']"), true},
		{"child action parent hook", base + networkHook("parent", "request", "['strip']") + networkAction("child", "strip"), true},
		{"grandparent action", base + "[permissions.parent]\nextends='grandparent'\n" + networkAction("grandparent", "strip") + networkHook("child", "request", "['strip']"), true},
		{"child hook replaces parent reference list", base + networkHook("parent", "request", "['missing']") + networkAction("child", "strip") + networkHook("child", "request", "['strip']"), true},
		{"child hook cannot remove another parent hook", base + networkHook("parent", "original", "['missing']") + networkAction("child", "strip") + networkHook("child", "request", "['strip']"), false},
		{"sibling action unavailable", base + "[permissions.parent]\n" + networkAction("sibling", "strip") + networkHook("child", "request", "['strip']"), false},
		{"empty parent cannot be repaired", base + "[permissions.parent.network.mitm.actions.strip]\n" + networkAction("child", "strip"), false},
		{"empty child cannot inherit operations", base + networkAction("parent", "strip") + "[permissions.child.network.mitm.actions.strip]\n", false},
		{"built-in parent adds no actions", "default_permissions='child'\n[permissions.child]\nextends=':workspace'\n" + networkHook("child", "request", "['missing']"), false},
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

func TestPermissionNetworkLayersAndManagedProfiles(t *testing.T) {
	selection := []byte("default_permissions='work'")
	action := []byte(networkAction("work", "strip"))
	hook := []byte(networkHook("work", "request", "['strip']"))
	for _, tc := range []struct {
		name     string
		base     []byte
		profiles [][]byte
		layers   ValidationLayers
		valid    bool
	}{
		{"layer supplies action", selection, [][]byte{hook}, ValidationLayers{Before: [][]byte{action}}, true},
		{"profiles independent", []byte("default_permissions='work'\n[permissions.work]"), [][]byte{action, hook}, ValidationLayers{}, false},
		{"later legacy disables references", append(bytes.Clone(selection), hook...), nil, ValidationLayers{After: [][]byte{[]byte("sandbox_mode='read-only'")}}, true},
		{"managed fallback resolves managed action", []byte("default_permissions='missing'"), nil, ValidationLayers{Requirements: []byte("default_permissions='work'\n[allowed_permission_profiles]\nwork=true\n" + networkAction("work", "strip") + networkHook("work", "request", "['strip']"))}, true},
		{"managed fallback bad reference", nil, nil, ValidationLayers{Requirements: []byte("default_permissions='work'\n[allowed_permission_profiles]\nwork=true\n" + networkHook("work", "request", "['missing']"))}, false},
		{"managed and user inheritance", []byte("default_permissions='child'\n[permissions.child]\nextends='parent'\n" + networkHook("child", "request", "['strip']")), nil, ValidationLayers{Requirements: []byte(networkAction("parent", "strip"))}, true},
		{"inactive managed definitions refused", []byte("default_permissions=':read-only'"), nil, ValidationLayers{Requirements: []byte("[permissions.unused.network.mitm.actions.empty]")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConfigSetWithLayers(t.Context(), SupportedConfigVersion, tc.base, tc.profiles, tc.layers)
			if tc.valid && err != nil || !tc.valid && !errors.Is(err, ErrValidation) {
				t.Fatal("unexpected layer validation", err)
			}
		})
	}
}

func TestPermissionNetworkCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, err := range []error{validateMITMDefinitions(ctx, nil), validateInheritedMITMActions(ctx, nil)} {
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
