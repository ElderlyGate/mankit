// Package split is the border between a dashboard's list and its detail pane:
// moved with keys or by dragging it, and kept across runs.
package split

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	MinList = 20 // the narrowest the list gets
	MinPane = 30 // the narrowest the pane gets
	Step    = 5  // cells one key press moves the border
)

// Split holds the list's width the user chose; the pane takes the rest, so a wider window widens the pane.
type Split struct {
	list     int // 0 leaves the app's own width
	file     string
	dragging bool
}

// File is <user config dir>/<app>/split, or "" when there is none.
func File(app string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, app, "split")
}

// Load reads the saved width; a missing or broken file leaves the app's own.
func Load(file string) Split {
	s := Split{file: file}
	if file == "" {
		return s
	}
	if b, err := os.ReadFile(file); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > 0 {
			s.list = n
		}
	}
	return s
}

// Left is the list's width in a window width cells wide; natural is the app's own choice.
func (s Split) Left(width, natural int) int {
	w := natural
	if s.list > 0 {
		w = s.list
	}
	if width < MinList+MinPane {
		return width / 2
	}
	return min(max(w, MinList), width-MinPane)
}

// Key moves the border: [ widens the pane, ] widens the list, = goes back to the app's own width.
func (s *Split) Key(key string, width, natural int) (handled bool, err error) {
	left := s.Left(width, natural)
	switch key {
	case "[":
		s.list = s.clampTo(left-Step, width)
	case "]":
		s.list = s.clampTo(left+Step, width)
	case "=":
		s.list = 0
	default:
		return false, nil
	}
	return true, s.save()
}

// Mouse drags the border between the boxes, rows top to bottom-1; it reports whether it used the event.
func (s *Split) Mouse(msg tea.MouseMsg, width, natural, top, bottom int) (used bool, err error) {
	left := s.Left(width, natural)
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		onBorder := (msg.X == left-1 || msg.X == left) && msg.Y >= top && msg.Y < bottom
		s.dragging = onBorder && width >= MinList+MinPane
		return s.dragging, nil
	case !s.dragging:
		return false, nil
	case msg.Action == tea.MouseActionMotion:
		s.list = s.clampTo(msg.X+1, width)
		return true, nil
	case msg.Action == tea.MouseActionRelease:
		s.dragging = false
		return true, s.save()
	}
	return true, nil
}

// Dragging is true while the border follows the mouse.
func (s Split) Dragging() bool { return s.dragging }

func (s Split) clampTo(w, width int) int {
	return min(max(w, MinList), width-MinPane)
}

func (s Split) save() error {
	if s.file == "" {
		return nil
	}
	if s.list == 0 {
		if err := os.Remove(s.file); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.file, []byte(strconv.Itoa(s.list)+"\n"), 0o644)
}
