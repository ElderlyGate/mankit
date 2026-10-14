package selection

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func mouse(a tea.MouseAction, x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: a, Button: tea.MouseButtonLeft}
}

func TestDragSelectsLikeATerminal(t *testing.T) {
	screen := "|abcdef|\n|ghijkl|\n|mnopqr|"
	box := Rect{1, 0, 7, 3}
	area := func(Point) Rect { return box }
	var d Drag
	if used, _ := d.Update(mouse(tea.MouseActionPress, 3, 0), area); used {
		t.Fatal("a press goes on to the click handlers")
	}
	d.Update(mouse(tea.MouseActionMotion, 9, 1), area) // past the box: clamped to it
	if got := d.Paint(screen); got == screen {
		t.Fatal("a drag under way is painted")
	}
	used, done := d.Update(mouse(tea.MouseActionRelease, 9, 1), area)
	if !used || done == nil {
		t.Fatal("a release after a drag hands back the selection")
	}
	if got := done.Text(screen); got != "cdef\nghijkl" {
		t.Fatalf("got %q", got)
	}
	if d.Paint(screen) != screen {
		t.Fatal("nothing is painted after the release")
	}
}

func TestClickSelectsNothing(t *testing.T) {
	var d Drag
	area := func(Point) Rect { return Rect{0, 0, 10, 10} }
	d.Update(mouse(tea.MouseActionPress, 2, 2), area)
	if used, done := d.Update(mouse(tea.MouseActionRelease, 2, 2), area); !used || done != nil {
		t.Fatalf("used %v done %v", used, done)
	}
}

func TestAreaIsTheBoxUnderThePointer(t *testing.T) {
	boxes := []Rect{{1, 1, 5, 5}, {6, 1, 10, 5}}
	if got := Area(boxes, Point{7, 2}, 20, 8); got != boxes[1] {
		t.Fatalf("got %v", got)
	}
	if got := Area(boxes, Point{0, 0}, 20, 8); got != (Rect{0, 0, 20, 8}) {
		t.Fatalf("outside the boxes it is the screen: %v", got)
	}
}
