// Package notice is the status line's latest message, put first for a few
// seconds: "copied 2 lines" shows even while a search, a running action or an
// older error holds the line, and a repeat of the same text shows again.
package notice

import (
	"time"

	"github.com/tugbayatilla/mankit/theme"
)

// For is how long a new notice comes first on the status line.
const For = 4 * time.Second

type Notice struct {
	Text string
	Err  bool
	at   time.Time
}

// Set replaces the notice; an empty text clears it.
func (n *Notice) Set(text string, isErr bool, now time.Time) {
	n.Text, n.Err, n.at = text, isErr, now
}

// Fresh is true while the notice is new enough to come first.
func (n Notice) Fresh(now time.Time) bool {
	return n.Text != "" && now.Sub(n.at) < For
}

// Render draws the notice: in the error colour, or picked out in the key
// colour while fresh so the eye catches the change.
func (n Notice) Render(t theme.Base, now time.Time) string {
	switch {
	case n.Err:
		return t.Error.Render(n.Text)
	case n.Fresh(now):
		return t.Key.Render(n.Text)
	}
	return n.Text
}
