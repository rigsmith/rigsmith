package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rigsmith/rigsmith/internal/shiprig/pipeline"
)

// rkey builds a rune key message; tkey a special-key message. Shared by the
// plan-editor and dashboard model tests (same package).
func rkey(s string) tea.KeyMsg      { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func tkey(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func editorSteps() []pipeline.ResolvedStep {
	return []pipeline.ResolvedStep{
		{Name: "version", IsBuiltin: true},
		{Name: "publish", IsBuiltin: true},
		{Name: "push", IsBuiltin: true, SkipReason: "filtered out"},
	}
}

func editorUpdate(m planEditorModel, msg tea.Msg) planEditorModel {
	nm, _ := m.Update(msg)
	return nm.(planEditorModel)
}

func TestPlanEditorToggleOffAndRun(t *testing.T) {
	m := newPlanEditor(editorSteps(), channelSelection{}, pipeline.NewSecretMasker())
	m = editorUpdate(m, tkey(tea.KeyDown)) // → publish
	m = editorUpdate(m, rkey("x"))         // toggle publish off

	m2, cmd := m.Update(tkey(tea.KeyEnter))
	m = m2.(planEditorModel)
	if !m.proceed {
		t.Fatal("enter should commit the run")
	}
	if cmd == nil {
		t.Error("enter should quit the program")
	}

	res := m.result()
	if res[0].SkipReason != "" {
		t.Errorf("version should run, got skip %q", res[0].SkipReason)
	}
	if res[1].SkipReason != editorSkipReason {
		t.Errorf("publish toggled off should be editor-disabled, got %q", res[1].SkipReason)
	}
	if res[2].SkipReason != "filtered out" {
		t.Errorf("push should keep its flag skip reason, got %q", res[2].SkipReason)
	}
}

func TestPlanEditorCancel(t *testing.T) {
	m := newPlanEditor(editorSteps(), channelSelection{}, pipeline.NewSecretMasker())
	m = editorUpdate(m, rkey("q"))
	if m.proceed {
		t.Error("q should cancel the release")
	}
}

func TestPlanEditorReEnableSkippedStep(t *testing.T) {
	m := newPlanEditor(editorSteps(), channelSelection{}, pipeline.NewSecretMasker())
	if m.steps[2].run {
		t.Fatal("a flag-skipped step should start toggled off")
	}
	m = editorUpdate(m, tkey(tea.KeyDown))
	m = editorUpdate(m, tkey(tea.KeyDown)) // → push
	m = editorUpdate(m, rkey("x"))         // re-enable push

	if res := m.result(); res[2].SkipReason != "" {
		t.Errorf("re-enabling push should clear its skip, got %q", res[2].SkipReason)
	}
}

func editorChannels() channelSelection {
	return channelSelection{all: []string{"osx-arm64", "osx-x64", "win-x64"}}
}

// editorDown moves the cursor down n rows.
func editorDown(m planEditorModel, n int) planEditorModel {
	for range n {
		m = editorUpdate(m, tkey(tea.KeyDown))
	}
	return m
}

func TestPlanEditorChannelsDefaultToAll(t *testing.T) {
	m := newPlanEditor(editorSteps(), editorChannels(), pipeline.NewSecretMasker())
	if len(m.chans) != 3 || m.checkedChannels() != 3 {
		t.Fatalf("every channel should start checked, got %+v", m.chans)
	}
	// All checked reads as "no restriction" — the build step's own default.
	if got := m.channels(); got != nil {
		t.Errorf("all channels checked should return nil, got %v", got)
	}
	if !strings.Contains(m.View(), "Build channels") {
		t.Error("the channel section should render when there are channels")
	}
}

func TestPlanEditorNoChannelSectionWithoutChannels(t *testing.T) {
	m := newPlanEditor(editorSteps(), channelSelection{}, pipeline.NewSecretMasker())
	if m.rows() != len(editorSteps()) {
		t.Errorf("the cursor should span steps only, got %d rows", m.rows())
	}
	if strings.Contains(m.View(), "Build channels") {
		t.Error("no channels means no channel section")
	}
	if m.channels() != nil {
		t.Error("a channel-less editor should never report channels")
	}
}

func TestPlanEditorPreselectsFlagChannels(t *testing.T) {
	sel := editorChannels()
	sel.picked = []string{"WIN-X64"} // --channels win-x64, however it was typed
	m := newPlanEditor(editorSteps(), sel, pipeline.NewSecretMasker())
	if got := m.channels(); len(got) != 1 || got[0] != "win-x64" {
		t.Errorf("the flag's channel should start checked alone, got %v", got)
	}
}

func TestPlanEditorNarrowsToOneChannel(t *testing.T) {
	m := newPlanEditor(editorSteps(), editorChannels(), pipeline.NewSecretMasker())
	m = editorDown(m, len(editorSteps())) // → first channel
	m = editorUpdate(m, rkey("x"))        // uncheck osx-arm64
	if got := m.channels(); len(got) != 2 || got[0] != "osx-x64" {
		t.Errorf("unchecking should drop that channel, got %v", got)
	}

	// "none" in the channel section means "only this one" — the one-keystroke
	// path to a single installer.
	m = editorDown(m, 2) // → win-x64
	m = editorUpdate(m, rkey("n"))
	if got := m.channels(); len(got) != 1 || got[0] != "win-x64" {
		t.Errorf("'n' should leave only the cursor's channel, got %v", got)
	}
	// …and the last checked channel can't be unchecked into an empty build.
	m = editorUpdate(m, rkey("x"))
	if got := m.channels(); len(got) != 1 || got[0] != "win-x64" {
		t.Errorf("the last channel should stay checked, got %v", got)
	}
	// Editing channels leaves the steps alone.
	for i, es := range m.steps {
		if es.run != editorSteps()[i].Enabled() {
			t.Errorf("step %d changed while editing channels", i)
		}
	}
	// "all" brings the whole matrix back.
	m = editorUpdate(m, rkey("a"))
	if got := m.channels(); got != nil {
		t.Errorf("'a' should restore every channel, got %v", got)
	}
}

func TestPlanEditorAllNone(t *testing.T) {
	m := newPlanEditor(editorSteps(), channelSelection{}, pipeline.NewSecretMasker())
	m = editorUpdate(m, rkey("n")) // none
	for i, es := range m.steps {
		if es.run {
			t.Errorf("step %d should be off after 'n'", i)
		}
	}
	m = editorUpdate(m, rkey("a")) // all
	for i, es := range m.steps {
		if !es.run {
			t.Errorf("step %d should be on after 'a'", i)
		}
	}
}
