package mergepolicy

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rigsmith/rigsmith/internal/agentrig/commitartifact"
	"github.com/rigsmith/rigsmith/internal/clauderig/devices"
	"github.com/rigsmith/rigsmith/internal/clauderig/manifest"
)

func metadataJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRetainedMetadataUsesNativeUnions(t *testing.T) {
	ours := manifest.Manifest{Schema: 1, SourceOS: "linux", Projects: map[string]manifest.Project{"ours": {Cwd: "/ours"}, "shared": {Cwd: "/ours/shared"}}}
	theirs := manifest.Manifest{Schema: 1, SourceOS: "windows", Projects: map[string]manifest.Project{"theirs": {Cwd: "/theirs"}, "shared": {Cwd: "/theirs/shared"}}}
	b, err := ResolveMetadata(t.Context(), manifest.FileName, nil, metadataJSON(t, ours), metadataJSON(t, theirs))
	if err != nil {
		t.Fatal(err)
	}
	var got manifest.Manifest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 3 || got.Projects["shared"].Cwd != "/ours/shared" || got.SourceOS != "windows" {
		t.Fatalf("manifest policy changed: %+v", got)
	}
	now := time.Now().UTC()
	a := devices.Registry{Schema: 1, Devices: map[string]devices.Device{"ours": {Name: "ours"}, "shared": {Name: "shared", LastSync: now, Account: &devices.Account{Email: "fixture@example.com"}}}}
	c := devices.Registry{Schema: 1, Devices: map[string]devices.Device{"theirs": {Name: "theirs"}, "shared": {Name: "shared", LastSync: now.Add(time.Hour)}}}
	b, err = ResolveMetadata(t.Context(), devices.FileName, nil, metadataJSON(t, a), metadataJSON(t, c))
	if err != nil {
		t.Fatal(err)
	}
	var registry devices.Registry
	if err := json.Unmarshal(b, &registry); err != nil {
		t.Fatal(err)
	}
	if len(registry.Devices) != 3 || registry.Devices["shared"].Account == nil || registry.Devices["shared"].Account.Email != "fixture@example.com" || !registry.Devices["shared"].LastSync.Equal(now.Add(time.Hour)) {
		t.Fatalf("device provenance lost: %+v", registry)
	}
}

func TestRetainedMetadataDeclinesUnsafeDocuments(t *testing.T) {
	valid := metadataJSON(t, manifest.Manifest{Schema: 1, SourceOS: "linux", Projects: map[string]manifest.Project{}})
	for _, raw := range []string{
		"null", "{}", "{", string(valid) + " {}", strings.Replace(string(valid), "\"schema\":1", "\"schema\":2", 1),
		strings.Replace(string(valid), "\"schema\":1", "\"schema\":1,\"schema\":1", 1),
		strings.Replace(string(valid), "\"schema\":1", "\"schema\":1,\"SCHEMA\":1", 1),
		strings.Replace(string(valid), "\"schema\":1", "\"schema\":1,\"future\":true", 1),
		strings.Replace(string(valid), "\"projects\":{}", "\"projects\":{\"p\":{\"cwd\":\"/p\",\"future\":true}}", 1),
	} {
		if _, err := ResolveMetadata(t.Context(), manifest.FileName, nil, []byte(raw), valid); !errors.Is(err, commitartifact.ErrConflict) {
			t.Fatalf("unsafe metadata accepted: %s %v", raw, err)
		}
	}
	for _, path := range []string{"cli/projects/p/s.jsonl", "settings.json", "nested/" + manifest.FileName} {
		if _, err := ResolveMetadata(t.Context(), path, nil, valid, valid); !errors.Is(err, commitartifact.ErrConflict) {
			t.Fatal("unsupported path accepted", path, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ResolveMetadata(ctx, manifest.FileName, nil, valid, valid); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// encoding/json treats long s as S/s and Kelvin sign as K/k. A lowercased
// duplicate check misses the former even though the decoder overwrites Schema.
func TestRetainedMetadataRejectsUnicodeAliases(t *testing.T) {
	valid := metadataJSON(t, manifest.Manifest{Schema: 1, SourceOS: "linux", Projects: map[string]manifest.Project{}})
	for _, fields := range []string{
		"\"schema\":2,\"ſchema\":1",
		"\"ſchema\":2,\"schema\":1",
		"\"SCHEMA\":2,\"ſchema\":1",
		"\"\\u017fchema\":2,\"schema\":1",
	} {
		raw := []byte("{" + fields + ",\"sourceOS\":\"linux\",\"projects\":{}}")
		var decoded manifest.Manifest
		if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Schema != 1 {
			t.Fatalf("invalid alias fixture: %+v %v", decoded, err)
		}
		if _, err := ResolveMetadata(t.Context(), manifest.FileName, nil, raw, valid); !errors.Is(err, commitartifact.ErrConflict) {
			t.Fatalf("accepted overwritten schema: %s %v", raw, err)
		}
	}
	for _, fields := range []string{
		"\"links\":{\"a\":\"one\"},\"linKs\":{\"a\":\"two\"}",
		"\"links\":{\"a\":\"one\"},\"linkſ\":{\"a\":\"two\"}",
	} {
		raw := []byte("{\"schema\":1,\"sourceOS\":\"linux\",\"projects\":{}," + fields + "}")
		if _, err := ResolveMetadata(t.Context(), manifest.FileName, nil, raw, valid); !errors.Is(err, commitartifact.ErrConflict) {
			t.Fatalf("accepted overwritten links: %s %v", raw, err)
		}
	}
}

func TestRetainedMetadataDeviceRemoval(t *testing.T) {
	old := devices.Device{Name: "retired", LastSync: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), Account: &devices.Account{Email: "fixture@example.com"}}
	registry := func(entries map[string]devices.Device) []byte {
		return metadataJSON(t, devices.Registry{Schema: 1, Devices: entries})
	}
	base := registry(map[string]devices.Device{"retired": old})
	empty := registry(map[string]devices.Device{})
	for _, changed := range []string{"unchanged", "synced", "account"} {
		survivor := old
		switch changed {
		case "synced":
			survivor.LastSync = old.LastSync.Add(time.Hour)
		case "account":
			survivor.Account = &devices.Account{Email: "changed@example.com"}
		}
		for _, reverse := range []bool{false, true} {
			a, b := empty, registry(map[string]devices.Device{"retired": survivor, "new": {Name: "new"}})
			if reverse {
				a, b = b, a
			}
			out, err := ResolveMetadata(t.Context(), devices.FileName, base, a, b)
			if err != nil {
				t.Fatal(err)
			}
			var got devices.Registry
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			_, kept := got.Devices["retired"]
			if kept != (changed != "unchanged") || !got.Has("new") {
				t.Errorf("%s reverse=%v: %+v", changed, reverse, got)
			}
		}
	}
	for _, bad := range [][]byte{[]byte{}, []byte("null"), []byte(`{"schema":2,"devices":{}}`), []byte(`{"schema":1,"devices":{},"future":true}`)} {
		if _, err := ResolveMetadata(t.Context(), devices.FileName, bad, empty, base); !errors.Is(err, commitartifact.ErrConflict) {
			t.Errorf("invalid base accepted: %s %v", bad, err)
		}
	}
}

func TestRetainedMetadataCaseSensitiveIdentifiers(t *testing.T) {
	for _, tc := range []struct{ path, raw string }{
		{manifest.FileName, `{"schema":1,"projects":{"Laptop":{"cwd":"/one"},"laptop":{"cwd":"/two"}},"links":{"s":"one","ſ":"two","k":"three","K":"four"}}`},
		{devices.FileName, `{"schema":1,"devices":{"Laptop":{"name":"Laptop"},"laptop":{"name":"laptop"}}}`},
	} {
		out, err := ResolveMetadata(t.Context(), tc.path, nil, []byte(tc.raw), []byte(tc.raw))
		if err != nil {
			t.Errorf("distinct identifiers rejected: %s %v", tc.path, err)
			continue
		}
		// Compare decoded values: serialization can add omitted zero fields.
		var want, got any
		if tc.path == manifest.FileName {
			want, got = &manifest.Manifest{}, &manifest.Manifest{}
		} else {
			want, got = &devices.Registry{}, &devices.Registry{}
		}
		if err := json.Unmarshal([]byte(tc.raw), want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(out, got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("identifiers or values changed: %s", out)
		}
	}
}

func TestRetainedMetadataNestedDuplicates(t *testing.T) {
	for _, tc := range []struct{ path, raw string }{
		{manifest.FileName, `{"schema":1,"projects":{"p":{},"p":{}}}`},
		{manifest.FileName, `{"schema":1,"links":{"p":"one","p":"two"}}`},
		{devices.FileName, `{"schema":1,"devices":{"p":{},"p":{}}}`},
		{manifest.FileName, `{"schema":1,"projects":{"p":{"cwd":"one","CWD":"two"}}}`},
		{devices.FileName, `{"schema":1,"devices":{"p":{"lastSync":null,"laſtSync":null}}}`},
		{devices.FileName, `{"schema":1,"devices":{"p":{"account":{"email":"one","EMAIL":"two"}}}}`},
	} {
		if _, err := ResolveMetadata(t.Context(), tc.path, nil, []byte(tc.raw), []byte(tc.raw)); !errors.Is(err, commitartifact.ErrConflict) {
			t.Errorf("nested duplicate accepted: %s %v", tc.raw, err)
		}
	}
}
