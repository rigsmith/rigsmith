package cli

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func twoTasks() []allTask {
	return []allTask{{name: "core", eco: "go"}, {name: "web", eco: "node"}}
}

func au(m allModel, msg tea.Msg) allModel {
	nm, _ := m.Update(msg)
	return nm.(allModel)
}

func TestAllModelLifecycle(t *testing.T) {
	m := newAllModel("build", twoTasks(), func() {})
	if m.rows[0].state != allPending {
		t.Fatal("rows start pending")
	}
	m = au(m, allStarted{0})
	if m.rows[0].state != allRunning || m.running != 0 {
		t.Fatalf("task 0 should be running, running=%d", m.running)
	}
	m = au(m, allOutput{"compiling…"})
	if len(m.output) != 1 {
		t.Errorf("output = %v", m.output)
	}
	m = au(m, allFinished{idx: 0, ok: true})
	if m.rows[0].state != allOK || m.okCount != 1 || m.running != -1 {
		t.Errorf("task 0 should be ok; okCount=%d running=%d", m.okCount, m.running)
	}
	m = au(m, allStarted{1})
	m = au(m, allFinished{idx: 1, ok: false})
	if m.rows[1].state != allFailed || m.failCount != 1 {
		t.Errorf("task 1 should be failed; failCount=%d", m.failCount)
	}
}

func TestAllModelOutputRing(t *testing.T) {
	m := newAllModel("test", twoTasks(), func() {})
	m = au(m, allStarted{0})
	for i := 0; i < maxAllOutput+4; i++ {
		m = au(m, allOutput{"line"})
	}
	if len(m.output) != maxAllOutput {
		t.Errorf("output should cap at %d, got %d", maxAllOutput, len(m.output))
	}
}

func TestAllModelDoneQuits(t *testing.T) {
	m := newAllModel("build", twoTasks(), func() {})
	m = au(m, allFinished{idx: 0, ok: true})
	nm, cmd := m.Update(allDoneMsg{})
	if !nm.(allModel).done {
		t.Error("allDoneMsg should mark done")
	}
	if cmd == nil {
		t.Error("allDoneMsg should quit")
	}
}

func TestAllModelCancel(t *testing.T) {
	cancelled := false
	m := newAllModel("build", twoTasks(), func() { cancelled = true })
	m = au(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.cancelled {
		t.Error("ctrl+c should set cancelled")
	}
	if !cancelled {
		t.Error("ctrl+c should invoke the cancel func")
	}
}

func TestAllModelSkipped(t *testing.T) {
	m := newAllModel("build", twoTasks(), func() {})
	m = au(m, allSkippedMsg{1})
	if m.rows[1].state != allSkipped {
		t.Error("allSkippedMsg should mark the row skipped")
	}
}

func TestLineSinkSplitsAndFlushes(t *testing.T) {
	var got []string
	s := &lineSink{emit: func(l string) { got = append(got, l) }}

	s.Write([]byte("hello\nwor"))
	s.Write([]byte("ld\r\nfoo")) // CR before LF is trimmed
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Fatalf("after writes, got %v", got)
	}
	s.flush() // trailing partial line
	if len(got) != 3 || got[2] != "foo" {
		t.Fatalf("after flush, got %v", got)
	}
}
