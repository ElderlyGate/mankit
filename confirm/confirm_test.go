package confirm

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestYAndEnterSayYes(t *testing.T) {
	y := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	n := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
	if !Yes(y) || !Yes(enter) || Yes(n) {
		t.Fatal("y and enter say yes, others cancel")
	}
}
