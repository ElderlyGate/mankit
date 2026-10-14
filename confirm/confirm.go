// Package confirm is how the dashboards ask before acting: y or enter says
// yes and any other key cancels. A strict question, where saying yes loses
// work, takes only y.
package confirm

import tea "github.com/charmbracelet/bubbletea"

// Yes reports whether k answers yes.
func Yes(k tea.KeyMsg, strict bool) bool {
	switch k.String() {
	case "y", "Y":
		return true
	case "enter":
		return !strict
	}
	return false
}

// Choice follows the question and says which keys answer it.
func Choice(strict bool) string {
	if strict {
		return " [y/N] type y"
	}
	return " [Y/n] enter = yes"
}
