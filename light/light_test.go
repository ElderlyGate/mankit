package light

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLightFollowsFocusAndRefresh(t *testing.T) {
	l, s, now := New(), Style{}, time.Now()
	if got := l.Render(s, l.Stale(now, time.Second)); got != "🟢" {
		t.Fatalf("a fresh start has the focus: %q", got)
	}
	if !l.Update(tea.BlurMsg{}) || !strings.HasPrefix(l.Render(s, false), "🟡") {
		t.Fatal("unfocused is yellow")
	}
	l.Update(tea.FocusMsg{})
	l.Refreshed(now.Add(-time.Minute))
	if !l.Stale(now, time.Second) || !strings.HasPrefix(l.Render(s, true), "🔴") {
		t.Fatal("a stale refresh is red")
	}
	if l.Update(tea.KeyMsg{}) {
		t.Fatal("other messages are not focus events")
	}
}
