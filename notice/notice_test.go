package notice

import (
	"testing"
	"time"

	"github.com/tugbayatilla/mankit/theme"
)

func TestNoticeIsFreshForAWhile(t *testing.T) {
	var n Notice
	now := time.Now()
	if n.Fresh(now) {
		t.Fatal("an empty notice is never fresh")
	}
	n.Set("copied line (1 line)", false, now)
	if !n.Fresh(now.Add(For-time.Millisecond)) || n.Fresh(now.Add(For)) {
		t.Fatal("fresh for For after Set")
	}
	n.Set("copied line (1 line)", false, now.Add(time.Minute))
	if !n.Fresh(now.Add(time.Minute)) {
		t.Fatal("the same text again is fresh again")
	}
	n.Set("", false, now)
	if n.Fresh(now) {
		t.Fatal("an empty text clears it")
	}
}

func TestRenderKeepsTheText(t *testing.T) {
	var n Notice
	now := time.Now()
	n.Set("✕ copy failed", true, now)
	if got := n.Render(theme.Mono, now.Add(time.Hour)); got == "" {
		t.Fatal("an error still renders after it stops being fresh")
	}
}
