// Package palette is the command palette (`:` or ctrl+p): type to search
// every command, ↑/↓ to pick, enter to run, space to star it as a favorite
// for the footer. An app keeps its own command type and what running means.
package palette

import (
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tugbayatilla/mankit/textinput"
)

// Palette lists the commands of type T that match what is typed.
type Palette[T any] struct {
	Input   textinput.Model
	Cursor  int
	Matches []T
}

// Open starts an empty search; filter it before the first render.
func Open[T any]() (*Palette[T], tea.Cmd) {
	in := textinput.New()
	in.Prompt = "› "
	in.Placeholder = "type to search commands"
	p := &Palette[T]{Input: in}
	return p, p.Input.Focus()
}

// Filter keeps the items whose text holds the query's letters in order.
func (p *Palette[T]) Filter(items []T, text func(T) string) {
	q := strings.ToLower(p.Input.Value())
	p.Matches = p.Matches[:0]
	for _, it := range items {
		if Subsequence(strings.ToLower(text(it)), q) {
			p.Matches = append(p.Matches, it)
		}
	}
	p.Cursor = min(max(p.Cursor, 0), max(len(p.Matches)-1, 0))
}

// Selected is the highlighted match; ok is false when nothing matches.
func (p *Palette[T]) Selected() (it T, ok bool) {
	if p.Cursor >= len(p.Matches) {
		return it, false
	}
	return p.Matches[p.Cursor], true
}

// Action is what a key did to the palette.
type Action int

const (
	Moved   Action = iota // the cursor moved
	Typed                 // the query changed: filter again
	Closed                // esc or ctrl+p
	Starred               // space: toggle the highlighted command as a favorite
	Chosen                // enter: run the highlighted command
)

// Key handles k. Space stars instead of typing, since labels need no spaces to find.
func (p *Palette[T]) Key(k tea.KeyMsg) (Action, tea.Cmd) {
	last := max(len(p.Matches)-1, 0)
	switch k.String() {
	case "esc", "ctrl+p":
		return Closed, nil
	case "up", "ctrl+k":
		p.Cursor = min(max(p.Cursor-1, 0), last)
		return Moved, nil
	case "down", "ctrl+j":
		p.Cursor = min(max(p.Cursor+1, 0), last)
		return Moved, nil
	case " ":
		return Starred, nil
	case "enter":
		return Chosen, nil
	}
	var cmd tea.Cmd
	p.Input, cmd = p.Input.Update(k)
	return Typed, cmd
}

// Row is how one command shows: its key, its label and whether it is starred.
type Row struct {
	Key, Label string
	Starred    bool
}

// Rows draws the input and the matches that fit in h lines of w cells.
func (p *Palette[T]) Rows(w, h int, row func(T) Row, selected, dim lipgloss.Style) []string {
	rows := []string{p.Input.View(), ""}
	room := max(h-len(rows), 1)
	off := max(0, p.Cursor-room+1)
	for i := off; i < len(p.Matches) && len(rows) < h; i++ {
		r := row(p.Matches[i])
		star := "  "
		if r.Starred {
			star = "★ "
		}
		line := ansi.Truncate(star+padRight(KeyName(r.Key), 7)+r.Label, w, "…")
		if i == p.Cursor {
			line = selected.Render(padRight(line, w))
		}
		rows = append(rows, line)
	}
	if len(p.Matches) == 0 {
		rows = append(rows, dim.Render("no command matches"))
	}
	return rows
}

func padRight(s string, w int) string {
	if n := ansi.StringWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// Subsequence reports whether s holds q's letters in order.
func Subsequence(s, q string) bool {
	for _, r := range q {
		i := strings.IndexRune(s, r)
		if i < 0 {
			return false
		}
		s = s[i+len(string(r)):]
	}
	return true
}

// KeyName is how a key shows in the palette and the footer.
func KeyName(k string) string {
	if k == " " {
		return "space"
	}
	return k
}

// KeyMsg is the key press for k, so running a command presses its key.
func KeyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+f":
		return tea.KeyMsg{Type: tea.KeyCtrlF}
	case "ctrl+p":
		return tea.KeyMsg{Type: tea.KeyCtrlP}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+x":
		return tea.KeyMsg{Type: tea.KeyCtrlX}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// Favorites are the commands the footer shows; the file holds one id per line.
type Favorites struct {
	IDs  map[string]bool
	File string // "" keeps them in memory only
}

// File is <user config dir>/<app>/favorites, or "" when there is none.
func File(app string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, app, "favorites")
}

// LoadFavorites reads file, or starts from defaults when it is missing.
func LoadFavorites(file string, defaults []string) Favorites {
	f := Favorites{IDs: map[string]bool{}, File: file}
	ids := defaults
	if file != "" {
		if b, err := os.ReadFile(file); err == nil {
			ids = strings.Fields(string(b))
		}
	}
	for _, id := range ids {
		f.IDs[id] = true
	}
	return f
}

// Toggle stars or unstars id and saves the favorites in order, the order of all ids.
func (f Favorites) Toggle(id string, order []string) error {
	f.IDs[id] = !f.IDs[id]
	if f.File == "" {
		return nil
	}
	var b strings.Builder
	for _, o := range order {
		if f.IDs[o] {
			b.WriteString(o + "\n")
		}
	}
	if err := os.MkdirAll(filepath.Dir(f.File), 0o755); err != nil {
		return err
	}
	return os.WriteFile(f.File, []byte(b.String()), 0o644)
}
