// Package header holds the pieces of the dashboards' top line.
package header

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Location shows path in room cells, home as ~. It shortens from the left so
// the last element (the repository, compose or spec file) always shows; dim
// paints the parent and title the last element.
func Location(path, home string, room int, dim, title lipgloss.Style) string {
	if home != "" && (path == home || strings.HasPrefix(path, home+string(filepath.Separator))) {
		path = "~" + strings.TrimPrefix(path, home)
	}
	parent, base := filepath.Split(path)
	if over := ansi.StringWidth(parent+base) - room; over > 0 && parent != "" {
		r := []rune(parent)
		parent = "…" + string(r[min(over+1, len(r)):]) // +1 for the …
	}
	return dim.Render(parent) + title.Render(base)
}
