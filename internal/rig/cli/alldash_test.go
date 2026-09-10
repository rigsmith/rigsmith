package cli

import (
	"strings"
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

func TestAllModelRowShowsPath(t *testing.T) {
	tasks := []allTask{{name: "@demo/core", eco: "node", rel: "examples/demo/node/core"}}
	m := newAllModel("build", tasks, func() {})
	v := m.View()
	if !strings.Contains(v, "examples/demo/node/core") {
		t.Errorf("row should show the package's path:\n%s", v)
	}
	if !strings.Contains(v, "node") {
		t.Errorf("row should still show the ecosystem:\n%s", v)
	}
}

// A package that doesn't define the verb is shown, dimmed, with the reason —
// and counted as skipped rather than ok or failed.
func TestAllModelSkippedShowsReasonAndCounts(t *testing.T) {
	tasks := []allTask{
		{name: "@acme/auth", eco: "node", rel: "packages/auth"},
		{name: "@acme/docs", eco: "node", rel: "apps/docs", skip: `no "typecheck" script`},
	}
	m := newAllModel("typecheck", tasks, func() {})
	m = au(m, allStarted{0})
	m = au(m, allFinished{idx: 0, ok: true})
	m = au(m, allSkippedMsg{1})
	m = au(m, allDoneMsg{})
	if m.okCount != 1 || m.failCount != 0 || m.skipCount != 1 {
		t.Fatalf("counts: ok=%d fail=%d skip=%d, want 1/0/1", m.okCount, m.failCount, m.skipCount)
	}
	v := m.View()
	if !strings.Contains(v, `no "typecheck" script`) {
		t.Errorf("skipped row should say why:\n%s", v)
	}
	if !strings.Contains(v, "✓ 1 ok") || !strings.Contains(v, "– 1 skipped") {
		t.Errorf("summary should report 1 ok and 1 skipped:\n%s", v)
	}
}
