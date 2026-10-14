// Package light is the header's traffic light, to tell several dashboards
// apart in tmux: green has the focus, yellow keeps refreshing in the
// background, red has stopped getting updates.
//
// Focus comes from the terminal's focus events: run the program with
// tea.WithReportFocus(); tmux passes them on only with `set -g focus-events on`.
package light

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Light is the zero value until New; a dashboard keeps one in its model.
type Light struct {
	focused   bool
	refreshed time.Time // when the last refresh landed
}

// New starts focused: terminals report only changes, and the program was
// just started in this pane.
func New() Light { return Light{focused: true} }

// Update follows focus and blur events and reports whether msg was one.
func (l *Light) Update(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.FocusMsg:
		l.focused = true
	case tea.BlurMsg:
		l.focused = false
	default:
		return false
	}
	return true
}

// Refreshed records a refresh that landed at now.
func (l *Light) Refreshed(now time.Time) { l.refreshed = now }

func (l Light) Focused() bool { return l.focused }

// Stale is true when the last refresh is older than after; never before the first.
func (l Light) Stale(now time.Time, after time.Duration) bool {
	return !l.refreshed.IsZero() && now.Sub(l.refreshed) > after
}

// Style colours the labels next to yellow and red.
type Style struct{ Dim, Error lipgloss.Style }

// Render is the light for the header; failing turns it red, as stale does.
func (l Light) Render(s Style, failing bool) string {
	switch {
	case failing:
		return "🔴 " + s.Error.Render("not updating")
	case !l.focused:
		return "🟡 " + s.Dim.Render("background")
	}
	return "🟢"
}
