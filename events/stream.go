package events

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/tugbayatilla/mankit/serve"
)

// Path is where the stream is served, one per server (ADR-E3).
const Path = "/api/events"

// route is GET /api/events; serve puts its guard in front, so there is no stream without the token, and none while the feature is off.
func (b *broker) route() serve.Route {
	return serve.Route{Pattern: http.MethodGet + " " + Path, Handler: b.stream}
}

// stream sends the kept events after Last-Event-ID, then each new one, with a comment on an idle line; it ends when the client leaves, the server stops or the feature is closed.
func (b *broker) stream(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	replay, ch, err := b.subscribe(r, r.Header.Get("Last-Event-ID"))
	if errors.Is(err, errTooManyStreams) {
		serve.WriteError(w, r, http.StatusServiceUnavailable, serve.Fail(CodeTooManyStreams, fmt.Sprintf("%d streams are open, the most this server keeps; try again later", b.cfg.Streams)))
		return
	}
	if err != nil {
		serve.WriteError(w, r, http.StatusInternalServerError, err)
		return
	}
	defer b.unsubscribe(ch)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	for _, text := range replay {
		if _, err := w.Write(text); err != nil {
			return
		}
	}
	if rc.Flush() != nil {
		return
	}
	beat := time.NewTicker(b.cfg.Heartbeat)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-serve.Stopping(r):
			return
		case text, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(text); err != nil || rc.Flush() != nil {
				return
			}
		case <-beat.C:
			if on, err := b.list.On(Feature.ID); err != nil || !on {
				b.forget()
				return
			}
			if _, err := w.Write([]byte(": heartbeat\n\n")); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
