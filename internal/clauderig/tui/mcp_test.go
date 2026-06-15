package tui

import (
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/mcp"
	"github.com/rigsmith/rigsmith/internal/clauderig/settings"
)

func sampleEntries() []mcp.Entry {
	return []mcp.Entry{
		{Name: "railway", Scope: settings.User, Server: mcp.Server{Command: "npx", Args: []string{"-y", "rmcp"}}},
		{Name: "docs", Scope: settings.Project, Server: mcp.Server{Type: mcp.TransportHTTP, URL: "https://x/mcp"}, State: mcp.StatePending},
	}
}

func TestMCP_AddHotkey(t *testing.T) {
	m, _ := NewMCP(sampleEntries(), "").Update(keyMsg("a"))
	if got := m.(MCPModel).Action; got.Kind != "add" {
		t.Fatalf("a → %+v, want Kind add", got)
	}
}

func TestMCP_RemoveTargetsCursor(t *testing.T) {
	m, _ := NewMCP(sampleEntries(), "").Update(keyMsg("x"))
	act := m.(MCPModel).Action
	if act.Kind != "remove" || act.Name != "railway" || act.Scope != settings.User {
		t.Fatalf("x on first row → %+v, want remove railway/user", act)
	}
}

// enable/disable only apply to project-scope servers; they're inert on a
// user-scope row.
func TestMCP_EnableDisableProjectOnly(t *testing.T) {
	base := NewMCP(sampleEntries(), "")
	if m, _ := base.Update(keyMsg("e")); m.(MCPModel).Action.Kind != "" {
		t.Error("enable should be inert on a user-scope row")
	}
	// move to the project row, then enable/disable should fire
	down, _ := base.Update(keyMsg("j"))
	en, _ := down.(MCPModel).Update(keyMsg("e"))
	if act := en.(MCPModel).Action; act.Kind != "enable" || act.Name != "docs" {
		t.Errorf("e on project row → %+v, want enable docs", act)
	}
	di, _ := down.(MCPModel).Update(keyMsg("d"))
	if act := di.(MCPModel).Action; act.Kind != "disable" || act.Name != "docs" {
		t.Errorf("d on project row → %+v, want disable docs", act)
	}
}

func TestMCP_QuitBacksOut(t *testing.T) {
	m, cmd := NewMCP(sampleEntries(), "").Update(keyMsg("q"))
	if m.(MCPModel).Action.Kind != "" {
		t.Error("q should record no action")
	}
	if cmd == nil {
		t.Error("q should return tea.Quit")
	}
}

func TestMCP_ViewRendersServersAndState(t *testing.T) {
	view := NewMCP(sampleEntries(), "added railway").View()
	for _, want := range []string{"MCP servers", "railway", "docs", "pending", "added railway", "https://x/mcp"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q\n%s", want, view)
		}
	}
}

func TestMCP_ViewEmptyState(t *testing.T) {
	if v := NewMCP(nil, "").View(); !strings.Contains(v, "no MCP servers") {
		t.Errorf("empty view should prompt to add\n%s", v)
	}
}

// Choosing an action erases the screen so the command's output (form/writes)
// starts clean — mirroring the dashboard.
func TestMCP_ClearsOnAction(t *testing.T) {
	m, _ := NewMCP(sampleEntries(), "").Update(keyMsg("a"))
	if m.(MCPModel).View() != "" {
		t.Error("screen should render empty after an action is chosen")
	}
}
