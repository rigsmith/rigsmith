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

// Only rigsmith's own engines may run the version step of a stackspace release;
// a custom run for the step is the user's business.
func TestRequireStackAwareTool(t *testing.T) {
	for tool, ok := range map[string]bool{
		"shiprig": true, "changerig": true, "changeset": true, "shiprig-dev": true,
		"/usr/local/bin/shiprig": true, `C:\tools\shiprig.exe`: true,
		"npx changeset": false, "pnpm changeset": false, "": false, "release-it": false,
	} {
		if got := stackAwareTool(tool); got != ok {
			t.Errorf("stackAwareTool(%q) = %v, want %v", tool, got, ok)
		}
	}
	if err := requireStackAwareTool(&pipeline.Config{Tool: "npx changeset"}); err == nil {
		t.Error("npx changeset accepted in a stackspace")
	}
	run := pipeline.CommandList{pipeline.ShellCommand("./my-version.sh")}
	if err := requireStackAwareTool(&pipeline.Config{Tool: "npx changeset", Steps: map[string]*pipeline.StepConfig{"version": {Run: run}}}); err != nil {
		t.Errorf("a custom version run should pass: %v", err)
	}
}
