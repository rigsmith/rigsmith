package detect

import "testing"

// Ported from the .NET rig's CapabilitiesTests.

func TestCapabilities_RunAndPublishUnavailableWithoutRunnableProjects(t *testing.T) {
	caps := Capabilities{HasSolution: true, RunnableProjects: 0, HasTestProject: true}
	if caps.Unavailable("run") == "" {
		t.Error("run should be unavailable")
	}
	if caps.Unavailable("publish") == "" {
		t.Error("publish should be unavailable")
	}
	if reason := caps.Unavailable("test"); reason != "" {
		t.Errorf("test should be available, got %q", reason)
	}
}

func TestCapabilities_TestAndCoverageUnavailableWithoutATestProject(t *testing.T) {
	caps := Capabilities{HasSolution: true, RunnableProjects: 2, HasTestProject: false}
	if caps.Unavailable("test") == "" {
		t.Error("test should be unavailable")
	}
	if caps.Unavailable("coverage") == "" {
		t.Error("coverage should be unavailable")
	}
	if reason := caps.Unavailable("run"); reason != "" {
		t.Errorf("run should be available, got %q", reason)
	}
}

func TestCapabilities_BuildRebuildKillAndCustomAreAlwaysAvailable(t *testing.T) {
	caps := Capabilities{HasSolution: false, RunnableProjects: 0, HasTestProject: false}
	for _, verb := range []string{"build", "rebuild", "kill", "deploy" /* custom verb */} {
		if reason := caps.Unavailable(verb); reason != "" {
			t.Errorf("%s should always be available, got %q", verb, reason)
		}
	}
}

func TestCapabilities_ProbeOnAnEmptyDirReportsNothingAvailable(t *testing.T) {
	caps := ProbeCapabilities(t.TempDir(), "", nil)
	if caps.HasSolution {
		t.Error("HasSolution should be false")
	}
	if caps.RunnableProjects != 0 {
		t.Errorf("RunnableProjects = %d, want 0", caps.RunnableProjects)
	}
	if caps.HasTestProject {
		t.Error("HasTestProject should be false")
	}
}
