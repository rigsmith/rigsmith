package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// setup does four things in order, and the first two are refusals. Both come
// before the engine, which is the point: acquiring it can be a multi-minute
// build, and neither of these states is going to need it.
func TestStackSetupRefusesBeforeItInstallsAnything(t *testing.T) {
	ctx := context.Background()

	t.Run("no manifest", func(t *testing.T) {
		inTempStackspace(t, "")
		err := runVerb(ctx, newStackSetupCmd())
		if err == nil || !strings.Contains(err.Error(), "rig stack init") {
			t.Fatalf("err = %v, want a pointer at init", err)
		}
	})

	t.Run("the untouched scaffold", func(t *testing.T) {
		// An empty manifest loads fine — it is what init writes. Setting up
		// nothing used to install the engine, generate a memberless README and
		// report success, which is a worse answer than the one this state has.
		inTempStackspace(t, "{\n  \"repos\": {}\n}\n")
		err := runVerb(ctx, newStackSetupCmd())
		if err == nil || !strings.Contains(err.Error(), "no repos yet") {
			t.Fatalf("err = %v, want the add-a-repo error", err)
		}
	})
}

// The menu runs a command that was never attached to a parent, so a step looked
// up by walking Parent() is a nil dereference — in the one place a first-timer
// is most likely to arrive from.
func TestStackSetupStepsResolveWithoutAParent(t *testing.T) {
	orphan := &cobra.Command{Use: "setup"}
	orphan.SetContext(context.Background())
	var out bytes.Buffer
	orphan.SetOut(&out)
	orphan.SetErr(&out)

	// status outside a stackspace is an error, not a panic: reaching its RunE
	// at all is what this asserts.
	if err := runStackSubcommand(orphan, "status"); err == nil && out.Len() == 0 {
		t.Fatal("the step did not run")
	}
	if err := runStackSubcommand(orphan, "nope"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("err = %v, want the internal missing-verb error", err)
	}
	// Every step setup names has to be one of them.
	for _, name := range []string{"init", "wire", "status"} {
		if _, ok := stackSteps[name]; !ok {
			t.Errorf("setup runs %q, which is not a known step", name)
		}
	}
}
