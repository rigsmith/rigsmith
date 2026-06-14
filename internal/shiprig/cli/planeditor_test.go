package cli

import (
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
	m := newPlanEditor(editorSteps(), pipeline.NewSecretMasker())
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
	m := newPlanEditor(editorSteps(), pipeline.NewSecretMasker())
	m = editorUpdate(m, rkey("q"))
	if m.proceed {
		t.Error("q should cancel the release")
	}
}

func TestPlanEditorReEnableSkippedStep(t *testing.T) {
	m := newPlanEditor(editorSteps(), pipeline.NewSecretMasker())
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

func TestPlanEditorAllNone(t *testing.T) {
	m := newPlanEditor(editorSteps(), pipeline.NewSecretMasker())
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
