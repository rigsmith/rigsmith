package cli

import (
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/rigsmith/core/brand"
	"github.com/rigsmith/rigsmith/internal/shiprig/pipeline"
)

// interactiveChooser is the bubbletea PlanChooser: it shows the resolved plan,
// lets the user toggle which steps run, then proceeds or cancels. Used only on
// an interactive terminal; non-interactive runs use pipeline.PassthroughChooser.
type interactiveChooser struct {
	in     io.Reader
	out    io.Writer
	masker *pipeline.SecretMasker
}

// Choose runs the editor and returns the steps with the user's toggles applied
// (SkipReason set to editorSkipReason for steps turned off, cleared for steps
// turned on) plus whether to proceed. On any error it falls back to the steps
// unchanged so a broken TTY can't strand a release.
func (c interactiveChooser) Choose(steps []pipeline.ResolvedStep) ([]pipeline.ResolvedStep, bool) {
	if len(steps) == 0 {
		return steps, true
	}
	m := newPlanEditor(steps, c.masker)
	opts := []tea.ProgramOption{tea.WithInput(c.in), tea.WithOutput(c.out)}
	res, err := tea.NewProgram(m, opts...).Run()
	if err != nil {
		return steps, true
	}
	final, ok := res.(planEditorModel)
	if !ok {
		return steps, true // unexpected final model → proceed with the plan unchanged
	}
	if !final.proceed {
		return nil, false
	}
	return final.result(), true
}

// editorSkipReason marks a step the user turned off in the plan editor.
const editorSkipReason = "disabled in plan editor"

type editorStep struct {
	step pipeline.ResolvedStep
	run  bool // current toggle state
}

type planEditorModel struct {
	steps  []editorStep
	cursor int
	masker *pipeline.SecretMasker

	proceed bool // set when the user commits the run
}

func newPlanEditor(steps []pipeline.ResolvedStep, masker *pipeline.SecretMasker) planEditorModel {
	es := make([]editorStep, len(steps))
	for i, s := range steps {
		es[i] = editorStep{step: s, run: s.Enabled()}
	}
	return planEditorModel{steps: es, masker: masker}
}

// result rebuilds the ResolvedStep slice with the toggles applied.
func (m planEditorModel) result() []pipeline.ResolvedStep {
	out := make([]pipeline.ResolvedStep, len(m.steps))
	for i, es := range m.steps {
		s := es.step
		if es.run {
			s.SkipReason = ""
		} else if s.SkipReason == "" {
			s.SkipReason = editorSkipReason
		}
		out[i] = s
	}
	return out
}

func (m planEditorModel) Init() tea.Cmd { return nil }

func (m planEditorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "q", "esc":
		m.proceed = false
		return m, tea.Quit
	case "enter", "g":
		m.proceed = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.steps)-1 {
			m.cursor++
		}
	case " ", "x":
		m.steps[m.cursor].run = !m.steps[m.cursor].run
	case "a":
		for i := range m.steps {
			m.steps[i].run = true
		}
	case "n":
		for i := range m.steps {
			m.steps[i].run = false
		}
	}
	return m, nil
}

var (
	editorTitle = lipgloss.NewStyle().Foreground(brand.AccentShip).Bold(true)
	editorOn    = lipgloss.NewStyle().Foreground(brand.Green)
	editorOff   = lipgloss.NewStyle().Foreground(brand.Muted)
	editorCur   = lipgloss.NewStyle().Foreground(brand.Cyan).Bold(true)
	editorDim   = lipgloss.NewStyle().Foreground(brand.Muted)
	editorGate  = lipgloss.NewStyle().Foreground(brand.Amber)
)

func (m planEditorModel) View() string {
	var b []byte
	b = append(b, editorTitle.Render("── Release plan — choose steps ──────────")...)
	b = append(b, '\n')

	for i, es := range m.steps {
		cursor := "  "
		if i == m.cursor {
			cursor = editorCur.Render("▸ ")
		}
		box := editorOff.Render("[ ]")
		name := editorOff.Render(es.step.Label())
		if es.run {
			box = editorOn.Render("[x]")
			name = es.step.Label()
		}
		line := cursor + box + " " + name
		// Annotations: a flag-based skip reason (when off and not user-disabled)
		// and a confirm-gate marker.
		if !es.run && es.step.SkipReason != "" && es.step.SkipReason != editorSkipReason {
			line += "  " + editorDim.Render("("+es.step.SkipReason+")")
		}
		if es.step.Confirm != nil {
			line += "  " + editorGate.Render("⏸ confirm")
		}
		b = append(b, line...)
		b = append(b, '\n')

		// Show the cursor step's action so the choice is informed.
		if i == m.cursor {
			for _, cmd := range planActionLines(es.step, m.masker) {
				b = append(b, editorDim.Render("      "+cmd)...)
				b = append(b, '\n')
			}
		}
	}

	b = append(b, '\n')
	b = append(b, editorDim.Render("↑/↓ move · space toggle · a all · n none · enter run · q cancel")...)
	b = append(b, '\n')
	return string(b)
}

// planActionLines renders a step's action as human-readable lines for the editor.
func planActionLines(s pipeline.ResolvedStep, masker *pipeline.SecretMasker) []string {
	if s.Kind == pipeline.StepKindScript {
		return []string{"(tengo script)"}
	}
	if s.Kind == pipeline.StepKindNative {
		return []string{"(" + pipeline.NativeStepDescription(s.Name) + ")"}
	}
	var out []string
	for _, c := range s.Action {
		out = append(out, "$ "+masker.Mask(pipeline.DescribeCommand(c)))
	}
	if len(out) == 0 {
		out = []string{editorDim.Render("(no action)")}
	}
	return out
}
