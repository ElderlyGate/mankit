package search

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func typeQuery(s *Search, q string) {
	s.Start()
	for _, r := range q {
		s.Key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func TestMarkFindsEveryHitCaseInsensitively(t *testing.T) {
	s := New("search: ")
	typeQuery(&s, "Go")
	mark := lipgloss.NewStyle()
	s.Mark("go\nnothing\nGopher go", mark, mark)
	if len(s.Matches) != 2 || s.Matches[1] != 2 || s.Query != "go" {
		t.Fatalf("matches %v query %q", s.Matches, s.Query)
	}
	if !s.Next(1) || s.Cur != 1 || !s.Next(1) || s.Cur != 0 {
		t.Fatalf("n wraps around: %d", s.Cur)
	}
	if l, ok := s.Line(); !ok || l != 0 {
		t.Fatalf("line %d %v", l, ok)
	}
	if !strings.Contains(s.Status(), "match 1/2") {
		t.Fatal(s.Status())
	}
}

func TestEnterKeepsEscDrops(t *testing.T) {
	s := New("search: ")
	typeQuery(&s, "x")
	s.Key(tea.KeyMsg{Type: tea.KeyEnter})
	if s.Typing || s.Query != "x" {
		t.Fatalf("enter keeps the query: %+v", s)
	}
	s.Start()
	s.Key(tea.KeyMsg{Type: tea.KeyEsc})
	if s.Typing || s.Query != "" || s.Status() != "" {
		t.Fatal("esc drops the query")
	}
}

func TestHighlightMarksEachOccurrence(t *testing.T) {
	got := Highlight("aXbx", "x", func(s ...string) string { return "[" + s[0] + "]" })
	if got != "a[X]b[x]" {
		t.Fatal(got)
	}
}
