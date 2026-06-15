package cli

import (
	"testing"

	"github.com/rigsmith/rigsmith/internal/shiprig/pipeline"
)

func TestConfigUsesEcosystems(t *testing.T) {
	none := &pipeline.Config{Steps: map[string]*pipeline.StepConfig{
		"publish": {Confirm: pipeline.ConfirmDefault()},
	}}
	if configUsesEcosystems(none) {
		t.Error("config without any ecosystems target should report false")
	}

	uses := &pipeline.Config{Steps: map[string]*pipeline.StepConfig{
		"smoke": {Ecosystems: []string{"node"}},
	}}
	if !configUsesEcosystems(uses) {
		t.Error("config with an ecosystems target should report true")
	}
}

func TestDistinctEcosystemsSortedDedupedNonNil(t *testing.T) {
	got := distinctEcosystems(map[string]string{
		"@acme/web": "node",
		"@acme/ui":  "node",
		"acme/cli":  "go",
		"orphan":    "", // packages with no ecosystem id are ignored
	})

	if len(got) != 2 || got[0] != "go" || got[1] != "node" {
		t.Errorf("distinctEcosystems = %v, want [go node]", got)
	}

	// An empty release must still yield a non-nil slice so filtering stays active.
	if empty := distinctEcosystems(map[string]string{}); empty == nil {
		t.Error("distinctEcosystems(empty) must be non-nil")
	}
}
