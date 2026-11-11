// Package events is an app's live events: CloudEvents 1.0 in structured JSON, checked against the app's catalog,
// sent on GET /api/events as Server-Sent Events through serve; it is a feature of its own, reached through the app's feature list.
package events

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/tugbayatilla/mankit/features"
)

// Feature is the events feature for the app's list: closed, there is no stream and nothing is kept.
var Feature = features.Feature{ID: "events", Title: "Live events", Covers: "GET /api/events, the live stream of what changed"}

// Event is one CloudEvent 1.0 in structured JSON with the distributed tracing extension (contracts event/v1).
type Event struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Subject         string          `json:"subject"`
	Time            time.Time       `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	Data            json.RawMessage `json:"data"`
	TraceParent     string          `json:"traceparent"`
	TraceState      string          `json:"tracestate,omitempty"`
}

// ErrNotInCatalog is a type the app did not declare; a program tells it by errors.Is, never by the message.
var ErrNotInCatalog = errors.New("not in the app's event catalog")

// Config is what an app tells events about itself.
type Config struct {
	Product   string         // the product ID: the first part of every type and of the source
	Thing     string         // the thing served, the source's second part (/<product>/<thing>); empty is Product
	Catalog   []string       // every type the app sends, <product>.<thing>.<verb> with the verb in the past
	Features  *features.List // the app's feature list; it must hold Feature
	Heartbeat time.Duration  // how often an idle stream gets a comment, and the feature is asked again; zero is 15s
	Keep      int            // how many events are kept for Last-Event-ID; zero is 256
}

// Broker builds an app's events and sends each to every open stream, keeping the last few for a stream that comes back.
type Broker struct {
	cfg    Config
	source string
	run    string

	mu   sync.Mutex
	seq  uint64
	kept []frame
	subs map[chan []byte]struct{}
}

// frame is one event as the stream sends it.
type frame struct {
	id   string
	text []byte
}

var (
	productPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	thingPattern   = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	typePattern    = regexp.MustCompile(`^[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*$`)
)

// New is the broker for cfg; it refuses a product, thing or catalog type that breaks the event names (ADR-E2), and a list without Feature.
func New(cfg Config) (*Broker, error) {
	if !productPattern.MatchString(cfg.Product) {
		return nil, fmt.Errorf("events: product %q is not a product ID", cfg.Product)
	}
	if cfg.Thing == "" {
		cfg.Thing = cfg.Product
	}
	if !thingPattern.MatchString(cfg.Thing) {
		return nil, fmt.Errorf("events: thing %q may hold only letters, digits, '.', '_' and '-'", cfg.Thing)
	}
	for _, t := range cfg.Catalog {
		if !typePattern.MatchString(t) || t[:len(cfg.Product)+1] != cfg.Product+"." {
			return nil, fmt.Errorf("events: catalog type %q is not %s.<thing>.<verb>", t, cfg.Product)
		}
	}
	if cfg.Features == nil {
		return nil, errors.New("events: needs the app's feature list")
	}
	if err := cfg.Features.Known(Feature.ID); err != nil {
		return nil, fmt.Errorf("events: the app's feature list must hold %s: %w", Feature.ID, err)
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = 15 * time.Second
	}
	if cfg.Keep <= 0 {
		cfg.Keep = 256
	}
	run := make([]byte, 8)
	if _, err := rand.Read(run); err != nil {
		return nil, err
	}
	return &Broker{
		cfg:    cfg,
		source: "/" + cfg.Product + "/" + cfg.Thing,
		run:    hex.EncodeToString(run),
		subs:   map[chan []byte]struct{}{},
	}, nil
}

// Publish builds the event typ about subject with data, traced by ctx (WithTrace), and sends it to every open stream.
// With the feature closed it builds nothing, keeps nothing and answers a zero Event; a store it cannot read is an error.
func (b *Broker) Publish(ctx context.Context, typ, subject string, data any) (Event, error) {
	if !slices.Contains(b.cfg.Catalog, typ) {
		return Event{}, fmt.Errorf("events: %q: %w", typ, ErrNotInCatalog)
	}
	if subject == "" {
		return Event{}, fmt.Errorf("events: %s needs the id of its thing as subject", typ)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, fmt.Errorf("events: the data of %s is not JSON: %w", typ, err)
	}
	on, err := b.cfg.Features.On(Feature.ID)
	if err != nil {
		return Event{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !on {
		b.closeAll()
		return Event{}, nil
	}
	b.seq++
	tp, ts := trace(ctx)
	e := Event{
		SpecVersion: "1.0", ID: b.run + "-" + strconv.FormatUint(b.seq, 10), Source: b.source, Type: typ, Subject: subject,
		Time: time.Now().UTC(), DataContentType: "application/json", Data: raw, TraceParent: tp, TraceState: ts,
	}
	text, err := json.Marshal(e)
	if err != nil {
		return Event{}, err
	}
	f := frame{id: e.ID, text: []byte("id: " + e.ID + "\ndata: " + string(text) + "\n\n")}
	b.kept = append(b.kept, f)
	if len(b.kept) > b.cfg.Keep {
		b.kept = slices.Delete(b.kept, 0, len(b.kept)-b.cfg.Keep)
	}
	for ch := range b.subs {
		select {
		case ch <- f.text:
		default:
			// A stream that cannot keep up is ended; it comes back with Last-Event-ID and gets what it missed.
			delete(b.subs, ch)
			close(ch)
		}
	}
	return e, nil
}

// subscribe opens a stream: the kept events after last (all of them when last is not kept), then every new one.
func (b *Broker) subscribe(last string) ([][]byte, chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	from := 0
	if last != "" {
		if i := slices.IndexFunc(b.kept, func(f frame) bool { return f.id == last }); i >= 0 {
			from = i + 1
		}
	} else {
		from = len(b.kept)
	}
	replay := make([][]byte, 0, len(b.kept)-from)
	for _, f := range b.kept[from:] {
		replay = append(replay, f.text)
	}
	ch := make(chan []byte, 64)
	b.subs[ch] = struct{}{}
	return replay, ch
}

func (b *Broker) unsubscribe(ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, open := b.subs[ch]; open {
		delete(b.subs, ch)
		close(ch)
	}
}

// forget ends every stream and every kept event, once the feature is found closed.
func (b *Broker) forget() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeAll()
}

// closeAll ends every stream and forgets every kept event, once the feature is closed.
func (b *Broker) closeAll() {
	for ch := range b.subs {
		delete(b.subs, ch)
		close(ch)
	}
	b.kept = nil
}

type traceKey struct{}

type traceContext struct{ parent, state string }

var traceParentPattern = regexp.MustCompile(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)

// WithTrace is ctx carrying the W3C trace context of the request that caused the events, such as its traceparent and tracestate headers;
// a traceparent that is not valid is left out, and the event starts a trace of its own.
func WithTrace(ctx context.Context, traceparent, tracestate string) context.Context {
	if !validTraceParent(traceparent) {
		return ctx
	}
	return context.WithValue(ctx, traceKey{}, traceContext{parent: traceparent, state: tracestate})
}

func validTraceParent(tp string) bool {
	return traceParentPattern.MatchString(tp) && tp[:2] != "ff" && tp[3:35] != "00000000000000000000000000000000" && tp[36:52] != "0000000000000000"
}

// trace is the trace context in ctx, or a new one: every event carries traceparent (ADR-E6).
func trace(ctx context.Context) (string, string) {
	if tc, ok := ctx.Value(traceKey{}).(traceContext); ok {
		return tc.parent, tc.state
	}
	for {
		id := make([]byte, 24)
		_, _ = rand.Read(id)
		tp := "00-" + hex.EncodeToString(id[:16]) + "-" + hex.EncodeToString(id[16:]) + "-00"
		if validTraceParent(tp) {
			return tp, ""
		}
	}
}
