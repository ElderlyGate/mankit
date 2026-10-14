package list

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestWrapKeepsShortTextOnOneLine(t *testing.T) {
	if got := Wrap("short", 20, 5); len(got) != 1 || got[0] != "short" {
		t.Fatalf("%q", got)
	}
}

func TestWrapIndentsLaterLines(t *testing.T) {
	got := Wrap("make the header show the traffic light before the name", 20, 10)
	if len(got) < 3 || !strings.HasPrefix(got[1], "  ") {
		t.Fatalf("%q", got)
	}
	for _, l := range got {
		if ansi.StringWidth(l) > 20 {
			t.Fatalf("line wider than the box: %q", l)
		}
	}
	if strings.Join(strings.Fields(strings.Join(got, " ")), " ") != "make the header show the traffic light before the name" {
		t.Fatalf("text lost: %q", got)
	}
}

func TestWrapCapsAndMarksTheCut(t *testing.T) {
	got := Wrap(strings.Repeat("word ", 40), 20, 3)
	if len(got) != 3 || !strings.HasSuffix(got[2], "…") {
		t.Fatalf("%q", got)
	}
}

func TestOffsetShowsTheWholeCursorRow(t *testing.T) {
	// 10 lines, cursor row 9 takes 3: rows 0..1 scroll off.
	if got := Offset(0, 9, 20, 10, 3); got != 2 {
		t.Fatalf("got %d", got)
	}
	// near the end the box stays full: 20 rows, last row takes 3 lines.
	if got := Offset(0, 19, 20, 10, 3); got != 12 {
		t.Fatalf("got %d", got)
	}
	if got := Offset(5, 3, 20, 10, 1); got != 3 {
		t.Fatalf("moving up scrolls up: %d", got)
	}
}

func TestRowAtSkipsTheWrappedLines(t *testing.T) {
	// off 2, cursor row 4 takes 3 lines on box lines 2..4.
	for y, want := range map[int]int{0: 2, 1: 3, 2: 4, 3: 4, 4: 4, 5: 5, 6: 6} {
		if got := RowAt(y, 2, 4, 3); got != want {
			t.Errorf("line %d: got row %d, want %d", y, got, want)
		}
	}
}

func TestWrapKeepsTheRowsSpacing(t *testing.T) {
	got := Wrap("now  (HEAD) a subject long enough to wrap twice over", 30, 5)
	if !strings.HasPrefix(got[0], "now  (HEAD)") {
		t.Fatalf("the first line keeps the columns of the other rows: %q", got)
	}
}
