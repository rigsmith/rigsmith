package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func dashUpdate(m dashboardModel, msg tea.Msg) (dashboardModel, tea.Cmd) {
	nm, cmd := m.Update(msg)
	return nm.(dashboardModel), cmd
}

func dash(m dashboardModel, msg tea.Msg) dashboardModel {
	nm, _ := dashUpdate(m, msg)
	return nm
}

// pumpForm drives the embedded huh confirm to its terminal state the way the
// Bubble Tea event loop would: run the command Update returned, feed the
// resulting message(s) back through Update, repeat. Accepting a huh confirm
// doesn't complete on the keypress — it schedules a NextField command that
// submits on the next cycle — so the test must run those commands. tea.BatchMsg
// is flattened; the loop is bounded as a safety net.
func pumpForm(m dashboardModel, cmd tea.Cmd) dashboardModel {
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 100; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		default:
			var next tea.Cmd
			m, next = dashUpdate(m, msg)
			queue = append(queue, next)
		}
	}
	return m
}

func TestDashboardStepLifecycle(t *testing.T) {
	m := newDashboardModel(editorSteps(), "shiprig")
	if m.steps[2].status != statusSkipped {
		t.Fatal("a flag-skipped step should start skipped")
	}

	m = dash(m, dashStepStarted{"version"})
	if m.steps[0].status != statusRunning || m.running != 0 {
		t.Fatalf("version should be running, running=%d", m.running)
	}
	m = dash(m, dashCmdStarted{"shiprig version"})
	if m.cmdLine != "shiprig version" {
		t.Errorf("cmdLine = %q", m.cmdLine)
	}
	m = dash(m, dashCmdOutput{[]string{"one", "two"}})
	if len(m.output) != 2 {
		t.Errorf("output = %v", m.output)
	}
	m = dash(m, dashStepCompleted{"version"})
	if m.steps[0].status != statusOK || m.running != -1 {
		t.Errorf("version should be ok and nothing running, running=%d", m.running)
	}
}

func TestDashboardOutputRingBuffer(t *testing.T) {
	m := newDashboardModel(editorSteps(), "shiprig")
	m = dash(m, dashStepStarted{"version"})
	for i := 0; i < maxDashOutput+5; i++ {
		m = dash(m, dashCmdOutput{[]string{"line"}})
	}
	if len(m.output) != maxDashOutput {
		t.Errorf("ring buffer should cap at %d, got %d", maxDashOutput, len(m.output))
	}
}

func TestDashboardConfirmFlow(t *testing.T) {
	m := newDashboardModel(editorSteps(), "shiprig")
	resp := make(chan bool, 1)

	m = dash(m, dashConfirm{message: "Proceed?", resp: resp})
	if !m.confirming {
		t.Fatal("should be awaiting confirmation")
	}
	m, cmd := dashUpdate(m, rkey("y"))
	m = pumpForm(m, cmd)
	if m.confirming {
		t.Error("answering should clear the confirm state")
	}
	select {
	case v := <-resp:
		if !v {
			t.Error("'y' should answer true")
		}
	default:
		t.Fatal("no answer delivered to the pipeline")
	}
}

func TestDashboardConfirmDecline(t *testing.T) {
	m := newDashboardModel(editorSteps(), "shiprig")
	resp := make(chan bool, 1)
	m = dash(m, dashConfirm{message: "Proceed?", resp: resp})
	m = dash(m, tkey(tea.KeyEsc))
	if v := <-resp; v {
		t.Error("esc should answer false (decline)")
	}
}

func TestDashboardCompletedQuitsAndRenders(t *testing.T) {
	m := newDashboardModel(editorSteps(), "shiprig")
	m, cmd := dashUpdate(m, dashRunCompleted{success: true, message: ""})
	if !m.done || !m.success {
		t.Fatal("run should be marked done+success")
	}
	if cmd == nil {
		t.Error("completion should quit the program")
	}
	if v := m.View(); !strings.Contains(v, "Release complete") {
		t.Errorf("final view missing success text:\n%s", v)
	}
}

func TestDashboardFailureShowsResumeHint(t *testing.T) {
	m := newDashboardModel(editorSteps(), "shiprig")
	m = dash(m, dashStepStarted{"publish"})
	m = dash(m, dashStepFailed{"publish"})
	m, _ = dashUpdate(m, dashRunCompleted{success: false, message: "boom"})
	v := m.View()
	if !strings.Contains(v, "Resume with: shiprig release --from publish") {
		t.Errorf("failure view missing resume hint:\n%s", v)
	}
}

func TestDashboardFailureSurfacesCommandOutput(t *testing.T) {
	m := newDashboardModel(editorSteps(), "shiprig")
	m = dash(m, dashStepStarted{"build"})
	m = dash(m, dashCmdOutput{[]string{"build App: vpk pack: exit 255: There is a release equal or greater to 1.0.0"}})
	m, _ = dashUpdate(m, dashRunCompleted{success: false, message: "step 'build' failed"})
	v := m.View()
	// The real error (the captured command output) must appear in the panel, not
	// just the generic step-failed message.
	if !strings.Contains(v, "There is a release equal or greater to 1.0.0") {
		t.Errorf("failure view dropped the command output (the real error):\n%s", v)
	}
}

func TestDashboardCancelUsesCancelPanel(t *testing.T) {
	m := newDashboardModel(editorSteps(), "shiprig")
	m = dash(m, dashStepStarted{"publish"})
	m = dash(m, dashStepCancelled{"publish"})
	m, _ = dashUpdate(m, dashRunCompleted{success: false, message: "Release stopped at the 'publish' confirm gate."})
	if !m.cancelled() {
		t.Error("a cancelled step should be detected")
	}
	if v := m.View(); !strings.Contains(v, "stopped") {
		t.Errorf("cancel view:\n%s", v)
	}
}
