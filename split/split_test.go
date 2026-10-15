package split

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLeftKeepsBothSidesUsable(t *testing.T) {
	var s Split
	if got := s.Left(120, 48); got != 48 {
		t.Fatalf("without a choice the app's own width stands: %d", got)
	}
	s.list = 200
	if got := s.Left(120, 48); got != 120-MinPane {
		t.Fatalf("the pane keeps %d cells: %d", MinPane, got)
	}
	s.list = 5
	if got := s.Left(120, 48); got != MinList {
		t.Fatalf("the list keeps %d cells: %d", MinList, got)
	}
	if got := s.Left(40, 48); got != 20 {
		t.Fatalf("too narrow for both minimums splits in half: %d", got)
	}
}

func TestChosenWidthStaysWhenTheWindowGrows(t *testing.T) {
	s := Split{list: 40}
	if s.Left(120, 48) != 40 || s.Left(240, 96) != 40 {
		t.Fatal("the list keeps its width, so the pane gets the new room")
	}
}

func TestKeysMoveTheBorderAndSave(t *testing.T) {
	file := filepath.Join(t.TempDir(), "app", "split")
	s := Load(file)
	if ok, err := s.Key("[", 120, 48); !ok || err != nil || s.Left(120, 48) != 48-Step {
		t.Fatalf("[ widens the pane: %v %v %d", ok, err, s.Left(120, 48))
	}
	if ok, _ := s.Key("]", 120, 48); !ok || s.Left(120, 48) != 48 {
		t.Fatalf("] widens the list: %d", s.Left(120, 48))
	}
	s.Key("[", 120, 48)
	if got := Load(file).Left(120, 48); got != 48-Step {
		t.Fatalf("the choice is kept across runs: %d", got)
	}
	if ok, err := s.Key("=", 120, 48); !ok || err != nil || s.Left(120, 48) != 48 {
		t.Fatal("= goes back to the app's own width")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("= removes the saved width: %v", err)
	}
	if ok, _ := s.Key("x", 120, 48); ok {
		t.Fatal("other keys are left to the app")
	}
}

func mouse(action tea.MouseAction, x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: action, Button: tea.MouseButtonLeft}
}

func TestDraggingTheBorder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "split")
	s := Load(file)
	if used, _ := s.Mouse(mouse(tea.MouseActionPress, 10, 5), 120, 48, 3, 20); used {
		t.Fatal("a press away from the border is left to the app")
	}
	if used, _ := s.Mouse(mouse(tea.MouseActionPress, 47, 5), 120, 48, 3, 20); !used || !s.Dragging() {
		t.Fatal("a press on the list's right border starts a drag")
	}
	s.Mouse(mouse(tea.MouseActionMotion, 30, 6), 120, 48, 3, 20)
	if got := s.Left(120, 48); got != 31 {
		t.Fatalf("the border follows the pointer: %d", got)
	}
	if used, err := s.Mouse(mouse(tea.MouseActionRelease, 30, 6), 120, 48, 3, 20); !used || err != nil || s.Dragging() {
		t.Fatal("release ends the drag")
	}
	if Load(file).Left(120, 48) != 31 {
		t.Fatal("release saves the width")
	}
	if used, _ := s.Mouse(mouse(tea.MouseActionPress, 31, 1), 120, 48, 3, 20); used {
		t.Fatal("the border is only grabbed between the boxes' rows")
	}
}
