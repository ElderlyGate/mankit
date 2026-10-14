package confirm

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestOnlyYAnswersAStrictQuestion(t *testing.T) {
	y := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	n := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
	if !Yes(y, false) || !Yes(enter, false) || Yes(n, false) {
		t.Fatal("y and enter say yes, others cancel")
	}
	if !Yes(y, true) || Yes(enter, true) {
		t.Fatal("a strict question takes only y")
	}
	if Choice(true) == Choice(false) {
		t.Fatal("the prompt tells a strict question apart")
	}
}
