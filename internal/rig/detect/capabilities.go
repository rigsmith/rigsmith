package detect

// Capabilities is what the current .NET repo supports, used to degrade
// gracefully (ported from the .NET rig): the menu hides verbs that can't apply
// (no test project → no test/coverage; no runnable project → no run/publish).
type Capabilities struct {
	HasSolution      bool
	RunnableProjects int
	HasTestProject   bool
}

// AllCapabilities is the permissive fallback when probing fails or doesn't
// apply, so a transient hiccup never hides verbs.
var AllCapabilities = Capabilities{HasSolution: true, RunnableProjects: 1, HasTestProject: true}

// ProbeCapabilities inspects the .NET projects at root. configuredSolution and
// exclude come from .rig.json when set.
func ProbeCapabilities(root, configuredSolution string, exclude []string) Capabilities {
	projects := DiscoverDotNet(root, configuredSolution, exclude)
	runnable := 0
	hasTest := false
	for _, p := range projects {
		if p.IsRunnable() {
			runnable++
		}
		if p.IsTest {
			hasTest = true
		}
	}
	return Capabilities{
		HasSolution:      FindSolution(root, configuredSolution) != "",
		RunnableProjects: runnable,
		HasTestProject:   hasTest,
	}
}

// Unavailable returns the reason a built-in verb can't run here, or "" if it
// can. build/rebuild/kill/custom verbs are always offered.
func (c Capabilities) Unavailable(verb string) string {
	switch verb {
	case "run", "publish", "default":
		if c.RunnableProjects == 0 {
			return "no runnable projects found"
		}
	case "test", "coverage":
		if !c.HasTestProject {
			return "no test project found"
		}
	}
	return ""
}
