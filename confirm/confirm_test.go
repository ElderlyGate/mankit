package confirm

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOnlyYSaysYes(t *testing.T) {
	runes := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	tests := []struct {
		name string
		key  tea.KeyMsg
		want bool
	}{
		{"y", runes("y"), true},
		{"Y", runes("Y"), true},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, false},
		{"n", runes("n"), false},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}, false},
	}
	for _, tt := range tests {
		if got := Yes(tt.key); got != tt.want {
			t.Errorf("Yes(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestChoiceSaysEnterIsNo(t *testing.T) {
	if got := Choice(); got != " [y/N] enter = no" {
		t.Fatalf("Choice() = %q", got)
	}
}
