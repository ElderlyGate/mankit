// Package selection is drag to copy: a left-button drag highlights text on the
// screen and releasing the button hands back what was selected.
package selection

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type Point struct{ X, Y int }

// Rect is a screen area: X0 ≤ x < X1, Y0 ≤ y < Y1.
type Rect struct{ X0, Y0, X1, Y1 int }

func (r Rect) Has(p Point) bool {
	return p.X >= r.X0 && p.X < r.X1 && p.Y >= r.Y0 && p.Y < r.Y1
}

func (r Rect) Clamp(p Point) Point {
	return Point{min(max(p.X, r.X0), r.X1-1), min(max(p.Y, r.Y0), r.Y1-1)}
}

// Area is the inside of the box at p, so a drag never picks up borders or the
// box next to it; outside the boxes it is the whole screen.
func Area(boxes []Rect, p Point, width, height int) Rect {
	for _, r := range boxes {
		if r.Has(p) {
			return r
		}
	}
	return Rect{0, 0, width, height}
}

// Selection runs from where the drag started to the pointer, inside the box it started in.
type Selection struct {
	From, To Point
	In       Rect
	Dragged  bool // the pointer moved with the button held; a plain click selects nothing
}

// cols is the column range selected on screen line y. Like a terminal, the
// first line runs to the box's right edge and the last starts at its left edge.
func (s Selection) cols(y int) (l, r int, ok bool) {
	a, b := s.From, s.To
	if b.Y < a.Y || (b.Y == a.Y && b.X < a.X) {
		a, b = b, a
	}
	if y < a.Y || y > b.Y {
		return 0, 0, false
	}
	l, r = s.In.X0, s.In.X1
	if y == a.Y {
		l = a.X
	}
	if y == b.Y {
		r = b.X + 1
	}
	return l, r, l < r
}

// Text is the selected part of screen as plain text, trailing spaces dropped.
func (s Selection) Text(screen string) string {
	var out []string
	for y, line := range strings.Split(screen, "\n") {
		if l, r, ok := s.cols(y); ok {
			out = append(out, strings.TrimRightFunc(ansi.Strip(ansi.Cut(line, l, r)), unicode.IsSpace))
		}
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

// Paint draws the selected cells in reverse video, which shows in every theme.
func (s Selection) Paint(screen string) string {
	lines := strings.Split(screen, "\n")
	for y, line := range lines {
		if l, r, ok := s.cols(y); ok {
			mid := ansi.Strip(ansi.Cut(line, l, r))
			lines[y] = ansi.Truncate(line, l, "") + "\x1b[0;7m" + mid + "\x1b[0m" + ansi.TruncateLeft(line, r, "")
		}
	}
	return strings.Join(lines, "\n")
}

// Drag follows one left-button drag at a time; the zero value is idle.
type Drag struct{ sel *Selection }

// Update follows msg. used reports whether it took the event; a press still
// goes on to the click handlers. done is the selection on a release after a
// drag, nil otherwise. area gives the box a drag starting at p stays in.
func (d *Drag) Update(msg tea.MouseMsg, area func(Point) Rect) (used bool, done *Selection) {
	p := Point{msg.X, msg.Y}
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		d.sel = &Selection{From: p, To: p, In: area(p)}
		return false, nil
	case d.sel == nil:
		return false, nil
	case msg.Action == tea.MouseActionMotion:
		d.sel.To, d.sel.Dragged = d.sel.In.Clamp(p), true
		return true, nil
	case msg.Action == tea.MouseActionRelease:
		s := *d.sel
		d.sel = nil
		if s.Dragged {
			return true, &s
		}
		return true, nil
	}
	return false, nil
}

// Paint draws the drag over the screen while it is under way.
func (d Drag) Paint(screen string) string {
	if d.sel == nil || !d.sel.Dragged {
		return screen
	}
	return d.sel.Paint(screen)
}
