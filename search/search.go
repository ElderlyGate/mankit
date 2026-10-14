// Package search finds text in a dashboard's pane: every hit marked, the
// current one apart, n/N between them. The query outlives the selection, so
// moving through the list keeps searching each new pane.
package search

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tugbayatilla/mankit/textinput"
)

type Search struct {
	Input   textinput.Model
	Typing  bool   // the query is being typed
	Query   string // lower case; "" when no search is active
	Matches []int  // pane line numbers holding the query
	Cur     int    // index into Matches
}

func New(prompt string) Search {
	in := textinput.New()
	in.Prompt = prompt
	in.CharLimit = 0
	return Search{Input: in}
}

// Mark finds the query in text and returns it with every hit marked, the
// current one with cur; without a query text comes back as it is.
func (s *Search) Mark(text string, hit, cur lipgloss.Style) string {
	if s.Query == "" {
		s.Matches = nil
		return text
	}
	lines := strings.Split(text, "\n")
	s.Matches = s.Matches[:0]
	for i, l := range lines {
		if strings.Contains(strings.ToLower(ansi.Strip(l)), s.Query) {
			s.Matches = append(s.Matches, i)
		}
	}
	s.Cur = min(max(s.Cur, 0), max(len(s.Matches)-1, 0))
	for n, i := range s.Matches {
		style := hit
		if n == s.Cur {
			style = cur
		}
		lines[i] = Highlight(ansi.Strip(lines[i]), s.Query, style.Render)
	}
	return strings.Join(lines, "\n")
}

// Highlight marks every case-insensitive occurrence of query in plain.
func Highlight(plain, query string, mark func(...string) string) string {
	lower := strings.ToLower(plain)
	if len(lower) != len(plain) {
		// Case folding changed byte offsets (rare scripts); mark the line.
		return mark(plain)
	}
	var b strings.Builder
	for {
		i := strings.Index(lower, query)
		if i < 0 {
			b.WriteString(plain)
			return b.String()
		}
		b.WriteString(plain[:i])
		b.WriteString(mark(plain[i : i+len(query)]))
		plain, lower = plain[i+len(query):], lower[i+len(query):]
	}
}

// Line is the pane line of the current hit; ok is false without hits.
func (s Search) Line() (line int, ok bool) {
	if len(s.Matches) == 0 {
		return 0, false
	}
	return s.Matches[s.Cur], true
}

// Next moves by step (+1 or -1), wrapping around; false when there is nothing to move to.
func (s *Search) Next(step int) bool {
	n := len(s.Matches)
	if n == 0 {
		return false
	}
	s.Cur = ((s.Cur+step)%n + n) % n
	return true
}

// Start opens the query for typing, with the cursor after the last one.
func (s *Search) Start() tea.Cmd {
	s.Typing = true
	s.Input.SetValue(s.Query)
	s.Input.CursorEnd()
	return s.Input.Focus()
}

func (s *Search) Clear() {
	s.Query = ""
	s.Input.SetValue("")
}

// Key edits the query while it is typed: enter keeps it, esc drops it.
// Afterwards the pane is marked again; while Typing, the first hit is current.
func (s *Search) Key(k tea.KeyMsg) tea.Cmd {
	switch k.Type {
	case tea.KeyEsc:
		s.Typing = false
		s.Input.Blur()
		s.Clear()
		return nil
	case tea.KeyEnter:
		s.Typing = false
		s.Input.Blur()
		return nil
	}
	var cmd tea.Cmd
	s.Input, cmd = s.Input.Update(k)
	s.Query = strings.ToLower(s.Input.Value())
	s.Cur = 0
	return cmd
}

// Status is the status line while a search is active; "" without one.
func (s Search) Status() string {
	if s.Query == "" {
		return ""
	}
	if len(s.Matches) == 0 {
		return fmt.Sprintf("no match for %q", s.Query)
	}
	return fmt.Sprintf("match %d/%d for %q · n/N next/prev · esc clears", s.Cur+1, len(s.Matches), s.Query)
}
