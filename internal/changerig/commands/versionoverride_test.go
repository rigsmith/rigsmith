package commands

import (
	"io"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/planner"
	"github.com/rigsmith/rigsmith/core/semver"
)

// Packages sharing a version file move together: two --release-as specs for
// them must agree.
func TestApplyReleaseAsRefusesTwoVersionsForOneVersionFile(t *testing.T) {
	shared := func() []*planner.Module {
		cur, _ := semver.Parse("1.0.0")
		return []*planner.Module{
			{Name: "pkg-a", DisplayName: "pkg-a", VersionFile: "Directory.Build.props", Current: cur},
			{Name: "pkg-b", DisplayName: "pkg-b", VersionFile: "Directory.Build.props", Current: cur},
		}
	}
	err := applyReleaseAs(io.Discard, shared(), []string{"pkg-a=2.0.0", "pkg-b=3.0.0"})
	if err == nil || !strings.Contains(err.Error(), "two versions") {
		t.Errorf("err = %v, want the two-versions refusal", err)
	}
	plan := shared()
	if err := applyReleaseAs(io.Discard, plan, []string{"pkg-a=2.0.0", "pkg-b=2.0.0"}); err != nil {
		t.Errorf("the same version twice: err = %v", err)
	}
	for _, m := range plan {
		if m.VersionOverride != "2.0.0" {
			t.Errorf("%s override = %q, want 2.0.0", m.Name, m.VersionOverride)
		}
	}
}
