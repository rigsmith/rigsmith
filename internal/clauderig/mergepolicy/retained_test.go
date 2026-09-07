package mergepolicy

import (
	"context"
	"encoding/json"
	"errors"
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
