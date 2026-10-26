// Package theme is the colour set every dashboard shares. An app's Theme
// embeds Base and adds its own colours, built from the same Colors, so a
// "good" green or a "warning" yellow is the same in every app.
//
// A theme is a named Palette (default, nord, gruvbox, solarized,
// high-contrast) in its dark-terminal or light-terminal variant, or mono.
// Resolve picks it from --theme, NO_COLOR and the app's config.yaml.
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

// Colors is one palette variant, as hex strings. Apps build their own
// styles from it.
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

// Dark and LightColors are the default palette (the hubman design), kept
// under their old names.
var (
	Dark        = DefaultPalette.Dark
	LightColors = DefaultPalette.Light
)

// Fg is a style in colour c.
func Fg(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }

// NewBase builds the shared styles from one palette variant. onHit is the
// text colour on search hits, which are always light.
func NewBase(name string, c Colors, onHit string) Base {
	return Base{
		Name:     name,
		Border:   Fg(c.Border),
		Active:   Fg(c.Accent),
		Title:    lipgloss.NewStyle().Bold(true),
		Selected: Fg(c.Text).Background(lipgloss.Color(c.Selection)),
		Dim:      Fg(c.Dim),
		Error:    Fg(c.Bad).Bold(true),
		Key:      Fg(c.Accent).Bold(true),
		Match:    Fg(onHit).Background(lipgloss.Color(c.Hit)),
		CurMatch: Fg(onHit).Background(lipgloss.Color(c.CurHit)).Bold(true),
	}
}

// Default suits dark terminals, Light light ones; Mono has no colour at all.
var (
	Default = NewBase("default", DefaultPalette.Dark, DefaultPalette.Light.Text)
	Light   = NewBase("light", DefaultPalette.Light, DefaultPalette.Light.Text)
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
