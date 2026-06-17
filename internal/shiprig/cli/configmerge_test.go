package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/jsonc"
	"github.com/rigsmith/rigsmith/internal/shiprig/pipeline"
)

// TestMergeUnified: merging a changeset config + a release config yields one
// document whose top level is the pipeline and whose `changeset` key is the
// changeset config — and both parse back out of it, with ecosystem blocks and
// unknown keys preserved.
func TestMergeUnified(t *testing.T) {
	changeset := []byte(`{
		"$schema": "https://rigsmith.dev/schemas/changeset-config.json",
		"versioning": { "source": "commits" },
		"ignore": ["*-demo"],
		"dotnet": { "versionStrategy": "lockstep" }
	}`)
	release := []byte(`{
		"tool": "shiprig",
		"order": ["version", "publish"]
	}`)

	merged, err := mergeUnified(changeset, release)
	if err != nil {
		t.Fatal(err)
	}

	// The release pipeline parses from the merged file's top level.
	pc, err := pipeline.ParseConfig(merged, ".", "merged")
	if err != nil {
		t.Fatalf("pipeline parse: %v", err)
	}
	if pc.Tool != "shiprig" || len(pc.Order) != 2 {
		t.Errorf("pipeline = %+v, want tool=shiprig order=[version publish]", pc)
	}

	// The changeset config parses from the `changeset` key.
	var keyed map[string]json.RawMessage
	if err := jsonc.Unmarshal(merged, &keyed); err != nil {
		t.Fatal(err)
	}
	csRaw, ok := keyed["changeset"]
	if !ok {
		t.Fatal("merged file has no `changeset` key")
	}
	cc, err := config.Parse(csRaw)
	if err != nil {
		t.Fatalf("changeset parse: %v", err)
	}
	if cc.Versioning.Source != config.SourceCommits {
		t.Errorf("versioning.source = %q, want commits", cc.Versioning.Source)
	}
	if len(cc.Ignore) != 1 || cc.Ignore[0] != "*-demo" {
		t.Errorf("ignore = %v, want [*-demo]", cc.Ignore)
	}
	// Ecosystem block survived the generic round-trip.
	if _, ok := cc.Ecosystems["dotnet"]; !ok {
		t.Error("dotnet ecosystem block was dropped in the merge")
	}

	// The unified file carries the shiprig $schema, and the per-config $schema
	// keys were stripped (not duplicated inside the changeset key).
	if !strings.Contains(string(merged), shiprigSchemaURL) {
		t.Error("merged file is missing the shiprig $schema header")
	}
	if strings.Contains(string(csRaw), "changeset-config.json") {
		t.Error("changeset $schema should be stripped from the nested key")
	}
}
