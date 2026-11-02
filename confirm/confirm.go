// Package confirm is how the dashboards ask before acting: y says yes and any other key, enter too, cancels.
package confirm

import tea "github.com/charmbracelet/bubbletea"

// Yes reports whether k answers yes.
func Yes(k tea.KeyMsg) bool {
	switch k.String() {
	case "y", "Y":
		return true
	}
	return false
}

// Choice follows the question and says which keys answer it.
func Choice() string { return " [y/N] enter = no" }
