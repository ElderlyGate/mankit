// Package events is an app's live events: CloudEvents 1.0 in structured JSON, checked against the app's catalog,
// sent on GET /api/events as Server-Sent Events through serve. It is a feature of its own: attached to the app's feature list,
// it hears of each change through serve, so no handler calls it and removing it touches only the list.
package events

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tugbayatilla/mankit/v2/features"
	"github.com/tugbayatilla/mankit/v2/serve"
)

// Feature is the events feature for the app's list: closed, there is no stream and nothing is kept.
// It starts off, as an enterprise feature (ADR-E9), so the app's store keeps the owner's on (features.Store.On).
var Feature = features.Feature{ID: "events", Title: "Live events", Covers: "GET /api/events, the live stream of what changed", StartsOff: true}

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

// Gapped ends the type of the event a stream gets when it missed events: <product>.stream.gapped, subject stream (contracts mankit/v1).
const Gapped = "stream.gapped"

// Gap is the data of the gapped event: the last event this server knows the client had (empty when it is not one of this server's),
// and the oldest event still kept (empty when none is). The client reloads its state.
type Gap struct {
	LastKnown  string `json:"lastKnown"`
	OldestKept string `json:"oldestKept,omitempty"`
}

// The codes of the errors events answers; a program decides by the code, never by the message.
const CodeTooManyStreams = "too_many_streams"

// ErrNotInCatalog is an event type the app did not declare; a program tells it by errors.Is, never by the message.
var ErrNotInCatalog = errors.New("not in the app's event catalog")

// Config is what an app tells events about itself.
type Config struct {
	Product   string           // the product ID: the first part of every type and of the source
	Thing     string           // the thing served, the source's second part (/<product>/<thing>); empty is Product
	Catalog   []string         // every type the app sends, <product>.<thing>.<verb> with the verb in the past
	Causes    map[string]Cause // the event each change route causes, by the route's pattern
	Heartbeat time.Duration    // how often an idle stream gets a comment, and the feature is asked again; zero is 15s
	Keep      int              // how many events are kept for Last-Event-ID; zero is 256
	Streams   int              // how many streams may be open at once; zero is 64
}

// Cause is the event a change route causes; the route's serve.Change gives the subject.
type Cause struct {
	Type string             // in the catalog, and its thing is the one the route changes
	Data func(data any) any // the event's data from the answer's data; nil is the answer's data
}

// broker builds an app's events and sends each to every open stream, keeping the last few for a stream that comes back.
type broker struct {
	cfg    Config
	list   *features.List
	source string
	log    io.Writer

	mu   sync.Mutex
	run  string
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

// Attach puts live events on the app's feature list, which must hold Feature; serve then adds GET /api/events and tells events of each change.
// It refuses a product, thing or type that breaks the event names (ADR-E2), and a cause whose type is not in the catalog.
func Attach(list *features.List, cfg Config) error {
	if !productPattern.MatchString(cfg.Product) {
		return fmt.Errorf("events: product %q is not a product ID", cfg.Product)
	}
	if cfg.Thing == "" {
		cfg.Thing = cfg.Product
	}
	if !thingPattern.MatchString(cfg.Thing) {
		return fmt.Errorf("events: thing %q may hold only letters, digits, '.', '_' and '-'", cfg.Thing)
	}
	for _, t := range cfg.Catalog {
		if !typePattern.MatchString(t) || !strings.HasPrefix(t, cfg.Product+".") {
			return fmt.Errorf("events: catalog type %q is not %s.<thing>.<verb>", t, cfg.Product)
		}
	}
	for pattern, c := range cfg.Causes {
		if !slices.Contains(cfg.Catalog, c.Type) {
			return fmt.Errorf("events: route %q causes %q: %w", pattern, c.Type, ErrNotInCatalog)
		}
	}
	if list == nil {
		return errors.New("events: needs the app's feature list")
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = 15 * time.Second
	}
	if cfg.Keep <= 0 {
		cfg.Keep = 256
	}
	if cfg.Streams <= 0 {
		cfg.Streams = 64
	}
	b := &broker{cfg: cfg, list: list, source: "/" + cfg.Product + "/" + cfg.Thing, log: os.Stderr, run: newRun(), subs: map[chan []byte]struct{}{}}
	if err := list.Attach(Feature.ID, b); err != nil {
		return fmt.Errorf("events: %w", err)
	}
	return nil
}

// newRun names a run of events: a stream that comes back with an id of another run missed events, and gets the gapped event.
func newRun() string {
	run := make([]byte, 8)
	_, _ = rand.Read(run)
	return hex.EncodeToString(run)
}

// Mount refuses a cause whose route is not a change of the thing its type names, and adds the stream.
func (b *broker) Mount(routes []serve.Route) ([]serve.Route, error) {
	for pattern, c := range b.cfg.Causes {
		i := slices.IndexFunc(routes, func(rt serve.Route) bool { return rt.Pattern == pattern })
		if i < 0 {
			return nil, fmt.Errorf("events: no route %q causes %s", pattern, c.Type)
		}
		if thing := strings.Split(c.Type, ".")[1]; routes[i].Changes.Thing != thing {
			return nil, fmt.Errorf("events: route %q causes %s, so it must say it changes %s", pattern, c.Type, thing)
		}
	}
	return []serve.Route{b.route()}, nil
}

// Changed sends the event the change's route causes. One that cannot be built is said on stderr, without its data,
// and takes its place as a gapped event, so a stream knows it missed one.
func (b *broker) Changed(a serve.Answered) {
	c, ok := b.cfg.Causes[a.Route.Pattern]
	if !ok {
		return
	}
	data := a.Data
	if c.Data != nil {
		data = c.Data(a.Data)
	}
	if err := b.publish(a.Request, c.Type, a.Route.Changes.ID(a.Data), data); err != nil {
		fmt.Fprintf(b.log, "events: %s from %s was not sent: %v\n", c.Type, a.Route.Pattern, err)
	}
}

// Skipped forgets everything: a change went by while the feature was off, so every stream that comes back gets the gapped event.
func (b *broker) Skipped(serve.Answered) { b.forget() }

// SwitchedOff lets go of every stream and kept event as soon as the owner switches the feature off.
func (b *broker) SwitchedOff() { b.forget() }

// publish builds the event typ about subject with data, traced by the request that caused it, and sends it to every open stream.
// An event that cannot be built takes its place as a gapped event.
func (b *broker) publish(r *http.Request, typ, subject string, data any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	id := b.run + "-" + strconv.FormatUint(b.seq, 10)
	why := "its subject is empty"
	if subject != "" {
		text, err := b.event(r, id, typ, subject, data)
		if err == nil {
			b.keep(frame{id: id, text: text})
			return nil
		}
		why = "its data is not JSON"
	}
	gap := Gap{}
	if len(b.kept) > 0 {
		gap.LastKnown = b.kept[len(b.kept)-1].id
	}
	text, err := b.event(r, id, b.cfg.Product+"."+Gapped, "stream", gap)
	if err != nil {
		return err
	}
	b.keep(frame{id: id, text: text})
	return fmt.Errorf("%s; streams got %s", why, Gapped)
}

// keep adds f to the kept events and sends it to every open stream.
func (b *broker) keep(f frame) {
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
}

// event is one event as a stream frame.
func (b *broker) event(r *http.Request, id, typ, subject string, data any) ([]byte, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	tp, ts := trace(r)
	text, err := json.Marshal(Event{
		SpecVersion: "1.0", ID: id, Source: b.source, Type: typ, Subject: subject,
		Time: time.Now().UTC(), DataContentType: "application/json", Data: raw, TraceParent: tp, TraceState: ts,
	})
	if err != nil {
		return nil, err
	}
	return []byte("id: " + id + "\ndata: " + string(text) + "\n\n"), nil
}

var errTooManyStreams = errors.New("too many streams")

// subscribe opens a stream: the kept events after last, then every new one. When last is not followed by what is kept,
// the stream gets the gapped event first, then every kept event; its id stands just before the oldest kept, so coming back with it is no gap.
func (b *broker) subscribe(r *http.Request, last string) ([][]byte, chan []byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.subs) >= b.cfg.Streams {
		return nil, nil, errTooManyStreams
	}
	oldest := b.seq + 1 - uint64(len(b.kept))
	var replay [][]byte
	switch seq, ours := b.position(last); {
	case last == "":
	case ours && seq+1 >= oldest && seq <= b.seq:
		for _, f := range b.kept[seq+1-oldest:] {
			replay = append(replay, f.text)
		}
	default:
		gap := Gap{}
		if ours && len(last) <= 128 {
			gap.LastKnown = last
		}
		if len(b.kept) > 0 {
			gap.OldestKept = b.kept[0].id
		}
		mark := make([]byte, 4)
		_, _ = rand.Read(mark)
		text, err := b.event(r, fmt.Sprintf("%s-%d-gap-%s", b.run, oldest-1, hex.EncodeToString(mark)), b.cfg.Product+"."+Gapped, "stream", gap)
		if err != nil {
			return nil, nil, err
		}
		replay = append(replay, text)
		for _, f := range b.kept {
			replay = append(replay, f.text)
		}
	}
	ch := make(chan []byte, 64)
	b.subs[ch] = struct{}{}
	return replay, ch, nil
}

// position is the place of id in this run's events: <run>-<seq>, or <run>-<seq>-gap-<mark> for a gapped event.
func (b *broker) position(id string) (uint64, bool) {
	run, rest, ok := strings.Cut(id, "-")
	if !ok || run != b.run {
		return 0, false
	}
	seq, _, _ := strings.Cut(rest, "-")
	n, err := strconv.ParseUint(seq, 10, 64)
	return n, err == nil
}

func (b *broker) unsubscribe(ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, open := b.subs[ch]; open {
		delete(b.subs, ch)
		close(ch)
	}
}

// forget ends every stream and every kept event and starts a new run, so a stream that comes back gets the gapped event.
func (b *broker) forget() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		delete(b.subs, ch)
		close(ch)
	}
	b.kept = nil
	b.run, b.seq = newRun(), 0
}

var traceParentPattern = regexp.MustCompile(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)

func validTraceParent(tp string) bool {
	return traceParentPattern.MatchString(tp) && tp[:2] != "ff" && tp[3:35] != "00000000000000000000000000000000" && tp[36:52] != "0000000000000000"
}

// trace is the W3C trace context of the request, or a new one when it has none or it is not valid: every event carries traceparent (ADR-E6).
func trace(r *http.Request) (string, string) {
	if tp := r.Header.Get("traceparent"); validTraceParent(tp) {
		return tp, r.Header.Get("tracestate")
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
