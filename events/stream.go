package events

import (
	"fmt"
	"net/http"
	"time"

	"github.com/tugbayatilla/mankit/serve"
)

// Path is where the stream is served, one per server (ADR-E3).
const Path = "/api/events"

// Route is GET /api/events for serve.Site.Routes, so serve's guard stands in front of it: no stream without the token.
func (b *Broker) Route() serve.Route {
	return serve.Route{Pattern: http.MethodGet + " " + Path, Handler: b.stream}
}

// stream sends the kept events after Last-Event-ID, then each new one, with a comment on an idle line; it ends when the client leaves, the server stops or the feature is closed.
func (b *Broker) stream(w http.ResponseWriter, r *http.Request) {
	if !b.open(w, r) {
		return
	}
	rc := http.NewResponseController(w)
	replay, ch := b.subscribe(r.Header.Get("Last-Event-ID"))
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
			if on, err := b.cfg.Features.On(Feature.ID); err != nil || !on {
				b.forget()
				return
			}
			if _, err := w.Write([]byte(": heartbeat\n\n")); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

// open answers 404 not_found while the feature is closed, as if the route were not there, and 500 when the switches cannot be read.
func (b *Broker) open(w http.ResponseWriter, r *http.Request) bool {
	on, err := b.cfg.Features.On(Feature.ID)
	if err != nil {
		serve.WriteError(w, r, http.StatusInternalServerError, serve.Fail(serve.CodeConfig, fmt.Sprintf("the feature %s cannot be read: %v", Feature.ID, err)))
		return false
	}
	if !on {
		b.forget()
		serve.WriteError(w, r, http.StatusNotFound, serve.Fail(serve.CodeNotFound, fmt.Sprintf("%s %s is not served: the feature %s is off", r.Method, Path, Feature.ID)))
		return false
	}
	return true
}
