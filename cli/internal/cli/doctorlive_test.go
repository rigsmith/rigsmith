package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func du(m doctorModel, msg tea.Msg) (doctorModel, tea.Cmd) {
	nm, cmd := m.Update(msg)
	return nm.(doctorModel), cmd
}

func TestDoctorModelLifecycle(t *testing.T) {
	m := newDoctorModel([]pendingCheck{{label: "go"}, {label: "node"}})
	if len(m.rows) != 2 || m.rows[0].done {
		t.Fatal("rows start pending")
	}

	m, cmd := du(m, checkDoneMsg{idx: 0, result: ok("go", "go1.26")})
	if !m.rows[0].done || m.severity != docOK {
		t.Errorf("after first check: done=%v severity=%d", m.rows[0].done, m.severity)
	}
	if cmd != nil {
		t.Error("not all checks done yet — should not quit")
	}

	m, cmd = du(m, checkDoneMsg{idx: 1, result: bad("node", "not found")})
	if !m.rows[1].done || m.severity != docError {
		t.Errorf("after second check: severity=%d, want error", m.severity)
	}
	if cmd == nil {
		t.Error("all checks done — should quit")
	}
	if v := m.View(); !strings.Contains(v, "problems found") || !strings.Contains(v, "go1.26") {
		t.Errorf("final view missing summary/detail:\n%s", v)
	}
}

func TestDoctorModelSeverityKeepsMax(t *testing.T) {
	m := newDoctorModel([]pendingCheck{{label: "a"}, {label: "b"}})
	m, _ = du(m, checkDoneMsg{idx: 0, result: warn("a", "meh")})
	m, _ = du(m, checkDoneMsg{idx: 1, result: ok("b", "fine")})
	if m.severity != docWarn {
		t.Errorf("severity should stay at warn, got %d", m.severity)
	}
}
