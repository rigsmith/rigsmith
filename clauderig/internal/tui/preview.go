package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rigsmith/core/brand"
)

// Preview is a scrollable confirmation over a block of text. It shows content in
// a viewport the user can scroll through, then proceeds or cancels on a hotkey.
// Like the dashboard, it only records the decision (Confirmed); the caller acts
// after the program exits, so no writing happens inside the event loop.
type Preview struct {
	title    string
	footer   string
	content  string
	vp       viewport.Model
	ready    bool
	quitting bool

	// Confirmed is the user's decision, read after the program exits.
	Confirmed bool
}

var (
	previewTitle  = lipgloss.NewStyle().Bold(true).Foreground(brand.AccentClaude)
	previewKey    = lipgloss.NewStyle().Bold(true).Foreground(brand.AccentClaude)
	previewScroll = lipgloss.NewStyle().Foreground(brand.Muted)
)

// NewPreview builds a scrollable preview of content under a title. footer is an
// optional one-line note shown above the key hints (e.g. the destination path).
func NewPreview(title, footer, content string) Preview {
	return Preview{title: title, footer: footer, content: content}
}

func (m Preview) Init() tea.Cmd { return nil }

// chromeHeight is the number of rows Preview paints around the viewport: the
// title line + blank, and the blank + footer + hints below.
const chromeHeight = 5

// Update handles sizing and hotkeys. y/enter/→ proceed; n/q/esc/ctrl+c cancel;
// everything else scrolls the viewport (arrows, j/k, page keys, g/G).
func (m Preview) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h := msg.Height - chromeHeight
		if h < 3 {
			h = 3
		}
		if !m.ready {
			m.vp = viewport.New(msg.Width, h)
			m.vp.SetContent(m.content)
			m.ready = true
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = h
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "enter", "right", "l":
			m.Confirmed = true
			m.quitting = true
			return m, tea.Quit
		case "n", "q", "esc", "ctrl+c":
			m.Confirmed = false
			m.quitting = true
			return m, tea.Quit
		}
	}
	if m.ready {
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Preview) View() string {
	if m.quitting {
		return ""
	}
	if !m.ready {
		return "\n  " + previewScroll.Render("loading…")
	}
	var b strings.Builder
	b.WriteString(previewTitle.Render(m.title) + "\n\n")
	b.WriteString(m.vp.View() + "\n")
	if m.footer != "" {
		b.WriteString(previewScroll.Render("  "+m.footer) + "\n")
	}
	b.WriteString(m.hints())
	return b.String()
}

// hints renders the scroll position and the action keys.
func (m Preview) hints() string {
	pos := "all"
	switch {
	case m.vp.AtTop() && m.vp.AtBottom():
	case m.vp.AtTop():
		pos = "top"
	case m.vp.AtBottom():
		pos = "end"
	default:
		pos = percent(m.vp.ScrollPercent())
	}
	return "  " + previewKey.Render("↑/↓") + " scroll  " +
		previewScroll.Render("("+pos+")") + "   " +
		previewKey.Render("y") + " proceed   " +
		previewKey.Render("n") + " cancel"
}

// percent formats a 0..1 scroll fraction as a clamped whole-percent string.
func percent(f float64) string {
	p := int(f*100 + 0.5)
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return strconv.Itoa(p) + "%"
}
