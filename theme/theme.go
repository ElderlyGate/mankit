// Package theme is the colour set every dashboard shares. An app's Theme
// embeds Base and adds its own colours, so t.Dim and t.Error read the same
// in each of them.
package theme

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

type Base struct {
	Name     string
	Border   lipgloss.Style
	Active   lipgloss.Style // border of the focused box
	Title    lipgloss.Style
	Selected lipgloss.Style
	Dim      lipgloss.Style
	Error    lipgloss.Style
	Key      lipgloss.Style
	Match    lipgloss.Style // search hit in the pane
	CurMatch lipgloss.Style // the hit n/N moved to
}

func fg(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }

// Default suits dark terminals, Light light ones; Mono has no colour at all.
var (
	Default = Base{
		Name:     "default",
		Border:   fg("8"),
		Active:   fg("6"),
		Title:    lipgloss.NewStyle().Bold(true),
		Selected: lipgloss.NewStyle().Reverse(true),
		Dim:      lipgloss.NewStyle().Faint(true),
		Error:    fg("1").Bold(true),
		Key:      fg("6").Bold(true),
		Match:    fg("0").Background(lipgloss.Color("3")),
		CurMatch: fg("0").Background(lipgloss.Color("208")).Bold(true),
	}
	Light = Base{
		Name:     "light",
		Border:   fg("250"),
		Active:   fg("25"),
		Title:    lipgloss.NewStyle().Bold(true),
		Selected: lipgloss.NewStyle().Reverse(true),
		Dim:      fg("244"),
		Error:    fg("124").Bold(true),
		Key:      fg("25").Bold(true),
		Match:    lipgloss.NewStyle().Background(lipgloss.Color("229")),
		CurMatch: lipgloss.NewStyle().Background(lipgloss.Color("214")).Bold(true),
	}
	Mono = Base{
		Name:     "mono",
		Border:   lipgloss.NewStyle(),
		Active:   lipgloss.NewStyle().Bold(true),
		Title:    lipgloss.NewStyle().Bold(true),
		Selected: lipgloss.NewStyle().Reverse(true),
		Dim:      lipgloss.NewStyle(),
		Error:    lipgloss.NewStyle().Bold(true),
		Key:      lipgloss.NewStyle().Bold(true),
		Match:    lipgloss.NewStyle().Underline(true),
		CurMatch: lipgloss.NewStyle().Reverse(true),
	}
)

// Named picks name from themes: "mono" under NO_COLOR, "default" for an
// unknown name, which ok reports.
func Named[T any](themes map[string]T, name string) (t T, ok bool) {
	if os.Getenv("NO_COLOR") != "" {
		return themes["mono"], true
	}
	if t, ok = themes[name]; !ok {
		return themes["default"], false
	}
	return t, true
}
