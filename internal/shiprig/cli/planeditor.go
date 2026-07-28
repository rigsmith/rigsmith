package cli

import (
	"io"
	"strings"

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

// channelSelection is the build-channel picker's input: every channel this
// release could build (empty for a repo with no channelled artifacts, which
// hides the section entirely) and which start checked — the `--channels` flag
// when it was given, otherwise all of them.
type channelSelection struct {
	all    []string
	picked []string
}

// Choose runs the editor and returns the steps with the user's toggles applied
// (SkipReason set to editorSkipReason for steps turned off, cleared for steps
// turned on) plus whether to proceed. On any error it falls back to the steps
// unchanged so a broken TTY can't strand a release.
func (c interactiveChooser) Choose(steps []pipeline.ResolvedStep) ([]pipeline.ResolvedStep, bool) {
	chosen, _, proceed := c.ChooseWithChannels(steps, channelSelection{})
	return chosen, proceed
}

// ChooseWithChannels is Choose plus the build-channel picker: it also returns
// the channels left checked, or nil when they all are — which the build step
// reads as "every channel", exactly like an omitted --channels.
func (c interactiveChooser) ChooseWithChannels(steps []pipeline.ResolvedStep, sel channelSelection) ([]pipeline.ResolvedStep, []string, bool) {
	if len(steps) == 0 {
		return steps, sel.picked, true
	}
	m := newPlanEditor(steps, sel, c.masker)
	opts := []tea.ProgramOption{tea.WithInput(c.in), tea.WithOutput(c.out)}
	res, err := tea.NewProgram(m, opts...).Run()
	if err != nil {
		return steps, sel.picked, true
	}
	final, ok := res.(planEditorModel)
	if !ok {
		return steps, sel.picked, true // unexpected final model → proceed with the plan unchanged
	}
	if !final.proceed {
		return nil, nil, false
	}
	return final.result(), final.channels(), true
}

// editorSkipReason marks a step the user turned off in the plan editor.
const editorSkipReason = "disabled in plan editor"

type editorStep struct {
	step pipeline.ResolvedStep
	run  bool // current toggle state
}

// editorChannel is one row of the build-channel section (a Velopack RID).
type editorChannel struct {
	name  string
	build bool
}

// planEditorModel drives one screen with two sections: the pipeline steps, then
// the build channels (absent unless this release builds a channelled artifact).
// The cursor runs over both — indices below len(steps) address a step, the rest
// address a channel — so one keymap serves the whole screen.
type planEditorModel struct {
	steps  []editorStep
	chans  []editorChannel
	cursor int
	masker *pipeline.SecretMasker

	proceed bool // set when the user commits the run
}

func newPlanEditor(steps []pipeline.ResolvedStep, sel channelSelection, masker *pipeline.SecretMasker) planEditorModel {
	es := make([]editorStep, len(steps))
	for i, s := range steps {
		es[i] = editorStep{step: s, run: s.Enabled()}
	}
	// No explicit pick means every channel is in play; an explicit one (the
	// --channels flag) starts with just those checked, so the editor shows what
	// the command line already asked for.
	picked := map[string]bool{}
	for _, ch := range sel.picked {
		picked[strings.ToLower(strings.TrimSpace(ch))] = true
	}
	cs := make([]editorChannel, len(sel.all))
	for i, ch := range sel.all {
		cs[i] = editorChannel{name: ch, build: len(picked) == 0 || picked[strings.ToLower(ch)]}
	}
	return planEditorModel{steps: es, chans: cs, masker: masker}
}

// channels returns the checked channels, or nil when every one is checked —
// "all channels" is the build step's default, and passing it explicitly would
// only make the run brittle if the config gains a channel later.
func (m planEditorModel) channels() []string {
	var out []string
	for _, c := range m.chans {
		if c.build {
			out = append(out, c.name)
		}
	}
	if len(out) == len(m.chans) {
		return nil
	}
	return out
}

// onChannel reports whether the cursor sits in the channel section.
func (m planEditorModel) onChannel() bool { return m.cursor >= len(m.steps) }

// rows is the total number of selectable lines across both sections.
func (m planEditorModel) rows() int { return len(m.steps) + len(m.chans) }

// checkedChannels counts the channels currently set to build.
func (m planEditorModel) checkedChannels() int {
	n := 0
	for _, c := range m.chans {
		if c.build {
			n++
		}
	}
	return n
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
		if m.cursor < m.rows()-1 {
			m.cursor++
		}
	case " ", "x":
		if m.onChannel() {
			i := m.cursor - len(m.steps)
			// Unchecking the last channel would build nothing while the build step
			// still reads as on — ignore it; `n` is how you narrow to one.
			if m.chans[i].build && m.checkedChannels() == 1 {
				break
			}
			m.chans[i].build = !m.chans[i].build
		} else {
			m.steps[m.cursor].run = !m.steps[m.cursor].run
		}
	// all/none act on the section the cursor is in, so one keymap serves both
	// lists. In the channel section "none" means "only this one" — narrowing to a
	// single installer is the whole point of the picker, and zero channels isn't
	// a state worth reaching.
	case "a":
		if m.onChannel() {
			for i := range m.chans {
				m.chans[i].build = true
			}
			break
		}
		for i := range m.steps {
			m.steps[i].run = true
		}
	case "n":
		if m.onChannel() {
			only := m.cursor - len(m.steps)
			for i := range m.chans {
				m.chans[i].build = i == only
			}
			break
		}
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

	// Build channels, when this release has any: the same toggle list, so a
	// release can be narrowed to one installer without leaving the editor.
	if len(m.chans) > 0 {
		b = append(b, '\n')
		b = append(b, editorTitle.Render("── Build channels ───────────────────────")...)
		b = append(b, '\n')
		for i, c := range m.chans {
			cursor := "  "
			if len(m.steps)+i == m.cursor {
				cursor = editorCur.Render("▸ ")
			}
			box, name := editorOff.Render("[ ]"), editorOff.Render(c.name)
			if c.build {
				box, name = editorOn.Render("[x]"), c.name
			}
			b = append(b, cursor+box+" "+name...)
			b = append(b, '\n')
		}
	}

	b = append(b, '\n')
	hint := "↑/↓ move · space toggle · a all · n none · enter run · q cancel"
	if m.onChannel() {
		hint = "↑/↓ move · space toggle · a all · n only this · enter run · q cancel"
	}
	b = append(b, editorDim.Render(hint)...)
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
