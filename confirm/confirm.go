// Package confirm is how the dashboards ask before acting: y or enter says
// yes and any other key cancels.
package confirm

import tea "github.com/charmbracelet/bubbletea"

// Yes reports whether k answers yes.
func Yes(k tea.KeyMsg) bool {
	switch k.String() {
	case "y", "Y", "enter":
		return true
	}
	return false
}

// Choice follows the question and says which keys answer it.
func Choice() string { return " [Y/n] enter = yes" }
