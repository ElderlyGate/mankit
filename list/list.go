// Package list is the dashboards' left-hand list, where the highlighted row
// shows its whole text: it wraps onto as many lines as it needs, up to half
// the box, while every other row stays one line cut with "…".
package list

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Indent is how far a wrapped row's later lines are moved in.
const Indent = 2

// Wrap lays plain text out in w cells: the first line full width, the rest
// indented. More than max lines end the last one with "…".
func Wrap(text string, w, max int) []string {
	text = strings.TrimRight(strings.ReplaceAll(text, "\t", "    "), " ")
	if w <= Indent+1 || max <= 1 || ansi.StringWidth(text) <= w {
		return []string{ansi.Truncate(text, w, "…")}
	}
	first := ansi.Wrap(text, w, "")
	head, rest, _ := strings.Cut(first, "\n")
	lines := []string{strings.TrimRight(head, " ")}
	rest = strings.Join(strings.Fields(rest), " ")
	pad := strings.Repeat(" ", Indent)
	for _, l := range strings.Split(ansi.Wrap(rest, w-Indent, ""), "\n") {
		lines = append(lines, pad+strings.TrimRight(l, " "))
	}
	if len(lines) > max {
		lines = lines[:max]
		lines[max-1] = ansi.Truncate(lines[max-1], w-1, "") + "…"
	}
	return lines
}

// MaxLines is how many lines the highlighted row may take in a box h lines high.
func MaxLines(h int) int { return max(h/2, 1) }

// Offset is the first row to draw so that row cur, curLines high, shows in
// full; off is the previous first row, n the number of rows, h the lines.
func Offset(off, cur, n, h, curLines int) int {
	curLines = min(max(curLines, 1), max(h, 1))
	if cur < off {
		off = cur
	}
	if cur-off+curLines > h {
		off = cur - h + curLines
	}
	last := max(n-(h-curLines+1), 0) // the first row that still fills the box
	return max(min(off, last), 0)
}

// RowAt is the row drawn on line y of the box, for the layout Offset gave;
// it may be past the end of the list.
func RowAt(y, off, cur, curLines int) int {
	switch top := cur - off; {
	case y < top:
		return off + y
	case y < top+curLines:
		return cur
	default:
		return off + y - (curLines - 1)
	}
}
