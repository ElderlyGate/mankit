// Package theme is the colour set every dashboard shares. An app's Theme
// embeds Base and adds its own colours, so t.Dim and t.Error read the same
// in each of them. An app takes its own colours from Dark or LightColors,
// so a "good" green or a "warning" yellow is the same in every app.
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

// Colors is the shared palette, as hex strings, the same one as the hubman
// design. Apps build their own styles from it.
type Colors struct {
	Text      string // text on Selection and on search hits
	Accent    string // focus, keys, links
	Dim       string // secondary text
	Border    string
	Good      string // ok, running, added
	Warn      string // warning, starting, drift
	Bad       string // failed, missing, deleted
	Info      string // hints, hunks, todo
	Blue      string // a second neutral hue: refs, YAML keys
	Selection string // background of the selected row
	Hit       string // background of a search hit
	CurHit    string // background of the current search hit
}

// Dark suits dark terminals, LightColors light ones.
var (
	Dark = Colors{
		Text:      "#e3e8e5",
		Accent:    "#7cc4ad",
		Dim:       "#7f8e87",
		Border:    "#4b5b54",
		Good:      "#7ed39b",
		Warn:      "#f0c86a",
		Bad:       "#f08a7a",
		Info:      "#73d0c9",
		Blue:      "#7fb2ff",
		Selection: "#2a3a34",
		Hit:       "#f0c86a",
		CurHit:    "#7cc4ad",
	}
	LightColors = Colors{
		Text:      "#1d2421",
		Accent:    "#2f6f5e",
		Dim:       "#5d6862",
		Border:    "#b9c1bb",
		Good:      "#2e7d4f",
		Warn:      "#9a6a00",
		Bad:       "#b3412f",
		Info:      "#1f6f8b",
		Blue:      "#2f5fb3",
		Selection: "#e1ede8",
		Hit:       "#f6e3a8",
		CurHit:    "#b9dccf",
	}
)

// Fg is a style in colour c.
func Fg(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }

func base(name string, c Colors) Base {
	return Base{
		Name:     name,
		Border:   Fg(c.Border),
		Active:   Fg(c.Accent),
		Title:    lipgloss.NewStyle().Bold(true),
		Selected: Fg(c.Text).Background(lipgloss.Color(c.Selection)),
		Dim:      Fg(c.Dim),
		Error:    Fg(c.Bad).Bold(true),
		Key:      Fg(c.Accent).Bold(true),
		Match:    Fg(LightColors.Text).Background(lipgloss.Color(c.Hit)),
		CurMatch: Fg(LightColors.Text).Background(lipgloss.Color(c.CurHit)).Bold(true),
	}
}

// Default suits dark terminals, Light light ones; Mono has no colour at all.
var (
	Default = base("default", Dark)
	Light   = base("light", LightColors)
	Mono    = Base{
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
