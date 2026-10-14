package palette

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type cmd struct{ id, key, label string }

var cmds = []cmd{{"fetch", "f", "fetch"}, {"reword", "w", "reword commit"}, {"quit", "q", "quit"}}

func text(c cmd) string { return c.label + " " + c.key }

func TestTypingFiltersBySubsequence(t *testing.T) {
	p, _ := Open[cmd]()
	p.Filter(cmds, text)
	if len(p.Matches) != 3 {
		t.Fatalf("an empty query lists everything: %v", p.Matches)
	}
	for _, r := range "rwd" {
		if a, _ := p.Key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}); a != Typed {
			t.Fatalf("a letter is typed: %v", a)
		}
		p.Filter(cmds, text)
	}
	if c, ok := p.Selected(); !ok || c.id != "reword" || len(p.Matches) != 1 {
		t.Fatalf("rwd finds reword: %v", p.Matches)
	}
}

func TestKeysMoveStarChooseClose(t *testing.T) {
	p, _ := Open[cmd]()
	p.Filter(cmds, text)
	cases := []struct {
		key  tea.KeyMsg
		want Action
	}{
		{tea.KeyMsg{Type: tea.KeyDown}, Moved},
		{tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}, Starred},
		{tea.KeyMsg{Type: tea.KeyEnter}, Chosen},
		{tea.KeyMsg{Type: tea.KeyEsc}, Closed},
	}
	for _, c := range cases {
		if a, _ := p.Key(c.key); a != c.want {
			t.Errorf("%s: got %v want %v", c.key, a, c.want)
		}
	}
	if c, _ := p.Selected(); c.id != "reword" {
		t.Fatalf("down moved to the second: %v", c)
	}
}

func TestRowsMarkFavoritesAndTheCursor(t *testing.T) {
	p, _ := Open[cmd]()
	p.Filter(cmds, text)
	rows := p.Rows(40, 10, func(c cmd) Row {
		return Row{Key: c.key, Label: c.label, Starred: c.id == "quit"}
	}, lipgloss.NewStyle(), lipgloss.NewStyle())
	if len(rows) != 5 || !strings.HasPrefix(rows[4], "★ q") {
		t.Fatalf("%q", rows)
	}
}

func TestFavoritesSaveInCommandOrder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "app", "favorites")
	f := LoadFavorites(file, []string{"quit"})
	if !f.IDs["quit"] {
		t.Fatal("a missing file starts from the defaults")
	}
	if err := f.Toggle("fetch", []string{"fetch", "reword", "quit"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != "fetch\nquit\n" {
		t.Fatalf("got %q", b)
	}
	if g := LoadFavorites(file, nil); !g.IDs["fetch"] || !g.IDs["quit"] {
		t.Fatal("the file wins over the defaults")
	}
}

func TestKeyMsgPressesTheKey(t *testing.T) {
	for _, k := range []string{"enter", "tab", "ctrl+f", " ", "q", "esc"} {
		if got := KeyMsg(k).String(); got != k {
			t.Errorf("%q: got %q", k, got)
		}
	}
}
