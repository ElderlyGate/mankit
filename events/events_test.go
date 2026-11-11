package events_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ElderlyGate/contracts"

	"github.com/tugbayatilla/mankit/ask"
	"github.com/tugbayatilla/mankit/events"
	"github.com/tugbayatilla/mankit/features"
	"github.com/tugbayatilla/mankit/serve"
)

var app = serve.App{ID: "shop", Version: "1.0.0", Contracts: []string{"v1"}}

const token = "s3cret"

var catalog = []string{"shop.item.added", "shop.item.removed"}

type item struct {
	ID string `json:"id"`
}

var (
	serveCreate = ask.Action{Type: ask.Create}
	serveDelete = ask.Action{Type: ask.Delete}
)

func itemID(data any) string { return data.(item).ID }

// routes are the shop's routes, the same with events and without: each change says the event it causes, and no handler sends one.
func routes() []serve.Route {
	return []serve.Route{
		{Pattern: "POST /api/items/{id}", Action: serveCreate, Causes: serve.Cause{Type: "shop.item.added", Subject: itemID}, Handler: func(w http.ResponseWriter, r *http.Request) {
			serve.Write(w, r, http.StatusCreated, "Item", item{ID: r.PathValue("id")})
		}},
		{Pattern: "DELETE /api/items/{id}", Action: serveDelete, Causes: serve.Cause{Type: "shop.item.removed", Subject: itemID, Data: func(any) any { return nil }}, Handler: func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("id") == "missing" {
				serve.WriteError(w, r, http.StatusNotFound, serve.Fail(serve.CodeNotFound, "no such item"))
				return
			}
			serve.Write(w, r, http.StatusOK, "Item", item{ID: r.PathValue("id")})
		}},
	}
}

// switches is the owner's switches, held in the test as an app's config would hold them.
type switches struct {
	mu   sync.Mutex
	off  []string
	fail error
}

func (s *switches) store() features.Store {
	return features.Store{
		Off: func() ([]string, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			return slices.Clone(s.off), s.fail
		},
		Keep: func(id string, off bool) error {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.off = slices.DeleteFunc(s.off, func(o string) bool { return o == id })
			if off {
				s.off = append(s.off, id)
			}
			return nil
		},
	}
}

func (s *switches) failing(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = err
}

type shop struct {
	list *features.List
	sw   *switches
	url  string
}

// serving is the shop behind serve's guard; with events, its list holds the feature and events is attached to it, and that is the only difference.
func serving(t *testing.T, keep int, withEvents bool) *shop {
	t.Helper()
	sw := &switches{}
	var all []features.Feature
	if withEvents {
		all = append(all, events.Feature)
	}
	list := features.New(all, sw.store())
	if withEvents {
		if err := events.Attach(list, events.Config{Product: "shop", Thing: "main", Catalog: catalog, Heartbeat: 20 * time.Millisecond, Keep: keep}); err != nil {
			t.Fatal(err)
		}
	}
	h, err := app.Handler(serve.Site{Routes: routes(), Features: list}, token, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &shop{list: list, sw: sw, url: srv.URL}
}

func (s *shop) do(t *testing.T, method, path string, header map[string]string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	req, _ := http.NewRequestWithContext(ctx, method, s.url+path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	return resp, cancel
}

// change makes a change with the user's yes and the token, as the page does, and gives the status.
func (s *shop) change(t *testing.T, method, path string, header map[string]string) int {
	t.Helper()
	h := map[string]string{app.TokenHeader(): token}
	for k, v := range header {
		h[k] = v
	}
	resp, cancel := s.do(t, method, path+"?confirm=yes", h)
	defer cancel()
	resp.Body.Close()
	return resp.StatusCode
}

func (s *shop) add(t *testing.T, id string) {
	t.Helper()
	if status := s.change(t, http.MethodPost, "/api/items/"+id, nil); status != http.StatusCreated {
		t.Fatalf("adding %s answered %d", id, status)
	}
}

// stream is an open GET /api/events.
type stream struct {
	resp *http.Response
	r    *bufio.Reader
	stop context.CancelFunc
}

func (s *shop) open(t *testing.T, lastID string) *stream {
	t.Helper()
	header := map[string]string{app.TokenHeader(): token}
	if lastID != "" {
		header["Last-Event-ID"] = lastID
	}
	resp, cancel := s.do(t, http.MethodGet, events.Path, header)
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, Content-Type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return &stream{resp: resp, r: bufio.NewReader(resp.Body), stop: cancel}
}

// next is the next event on the stream, with its id: line checked against the event's id; it skips heartbeat comments.
func (st *stream) next(t *testing.T) (events.Event, []byte) {
	t.Helper()
	var id, data string
	for {
		line, err := st.r.ReadString('\n')
		if err != nil {
			t.Fatalf("the stream ended: %v", err)
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "" && data != "":
			var e events.Event
			if err := json.Unmarshal([]byte(data), &e); err != nil {
				t.Fatal(err)
			}
			if e.ID != id {
				t.Fatalf("id: %q, the event's id %q", id, e.ID)
			}
			return e, []byte(data)
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func (st *stream) ids(t *testing.T, n int) []string {
	t.Helper()
	var out []string
	for range n {
		e, _ := st.next(t)
		out = append(out, e.ID)
	}
	return out
}

// ended says whether the stream ends without another event.
func (st *stream) ended() bool {
	for {
		line, err := st.r.ReadString('\n')
		if err != nil {
			return true
		}
		if strings.HasPrefix(line, "data: ") {
			return false
		}
	}
}

func refusal(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()
	var env serve.Envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != status || len(env.Errors) != 1 || env.Errors[0].Code != code {
		t.Fatalf("status %d, errors %+v; want %d %s", resp.StatusCode, env.Errors, status, code)
	}
}

// Success

func TestAChangeSendsItsEventToEveryOpenStream(t *testing.T) {
	s := serving(t, 0, true)
	a, b := s.open(t, ""), s.open(t, "")
	s.add(t, "item-1")
	for _, st := range []*stream{a, b} {
		e, _ := st.next(t)
		if e.Type != "shop.item.added" || e.Source != "/shop/main" || e.Subject != "item-1" || string(e.Data) != `{"id":"item-1"}` {
			t.Fatalf("event %+v", e)
		}
	}
}

func TestARouteMayShapeTheEventsData(t *testing.T) {
	s := serving(t, 0, true)
	st := s.open(t, "")
	s.change(t, http.MethodDelete, "/api/items/item-1", nil)
	if e, _ := st.next(t); e.Type != "shop.item.removed" || string(e.Data) != "null" {
		t.Fatalf("event %+v", e)
	}
}

func TestAnIdleStreamGetsAHeartbeat(t *testing.T) {
	s := serving(t, 0, true)
	st := s.open(t, "")
	if line, err := st.r.ReadString('\n'); err != nil || line != ": heartbeat\n" {
		t.Fatalf("line %q, %v", line, err)
	}
}

func TestTheTraceOfTheRequestIsCarried(t *testing.T) {
	s := serving(t, 0, true)
	st := s.open(t, "")
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	s.change(t, http.MethodPost, "/api/items/item-1", map[string]string{"traceparent": tp, "tracestate": "shop=1"})
	s.change(t, http.MethodPost, "/api/items/item-2", map[string]string{"traceparent": "00-" + strings.Repeat("0", 32) + "-00f067aa0ba902b7-01", "tracestate": "x"})
	if e, _ := st.next(t); e.TraceParent != tp || e.TraceState != "shop=1" {
		t.Fatalf("event %+v", e)
	}
	if e, _ := st.next(t); e.TraceParent == "" || strings.Contains(e.TraceParent, strings.Repeat("0", 32)) || e.TraceState != "" {
		t.Fatalf("an invalid traceparent was carried: %+v", e)
	}
}

func TestAnAppWithoutEventsServesTheSameRoutes(t *testing.T) {
	s := serving(t, 0, false)
	s.add(t, "item-1")
	resp, cancel := s.do(t, http.MethodGet, events.Path, map[string]string{app.TokenHeader(): token})
	defer cancel()
	defer resp.Body.Close()
	refusal(t, resp, http.StatusNotFound, serve.CodeNotFound)
}

// Contract

func TestEveryEventMeetsTheContract(t *testing.T) {
	s := serving(t, 1, true)
	live := s.open(t, "")
	s.add(t, "item-1")
	s.change(t, http.MethodPost, "/api/items/item-2", map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "tracestate": "shop=1"})
	s.change(t, http.MethodDelete, "/api/items/item-1", nil)
	var sent [][]byte
	var first string
	for i := range 3 {
		e, data := live.next(t)
		if i == 0 {
			first = e.ID
		}
		sent = append(sent, data)
	}
	gap := s.open(t, first)
	for range 2 {
		_, data := gap.next(t)
		sent = append(sent, data)
	}
	for _, data := range sent {
		if err := contracts.Validate("event/v1/event.schema.json", data); err != nil {
			t.Fatalf("%s: %v", data, err)
		}
	}
}

// Resume

func TestLastEventIDResumesAfterTheEventGiven(t *testing.T) {
	s := serving(t, 0, true)
	live := s.open(t, "")
	s.add(t, "item-1")
	s.add(t, "item-2")
	s.add(t, "item-3")
	sent := live.ids(t, 3)
	st := s.open(t, sent[0])
	if got := st.ids(t, 2); !slices.Equal(got, sent[1:]) {
		t.Fatalf("ids %v, want %v", got, sent[1:])
	}
	s.add(t, "item-4")
	if e, _ := st.next(t); e.Subject != "item-4" {
		t.Fatalf("event %+v, want the new one", e)
	}
}

func TestALastEventIDNoLongerKeptGetsAGapThenEveryEventKept(t *testing.T) {
	s := serving(t, 2, true)
	live := s.open(t, "")
	s.add(t, "item-1")
	s.add(t, "item-2")
	s.add(t, "item-3")
	s.add(t, "item-4")
	sent := live.ids(t, 4)
	st := s.open(t, sent[0])
	gap, _ := st.next(t)
	var data events.Gap
	if err := json.Unmarshal(gap.Data, &data); err != nil {
		t.Fatal(err)
	}
	if gap.Type != "shop."+events.GapVerb || data != (events.Gap{LastKnown: sent[0], OldestKept: sent[2]}) {
		t.Fatalf("gap %+v, data %+v", gap, data)
	}
	if got := st.ids(t, 2); !slices.Equal(got, sent[2:]) {
		t.Fatalf("ids %v, want %v", got, sent[2:])
	}
	again := s.open(t, gap.ID)
	if got := again.ids(t, 2); !slices.Equal(got, sent[2:]) {
		t.Fatalf("coming back with the gap's id gave %v, want %v without another gap", got, sent[2:])
	}
}

func TestALastEventIDFromAnotherServerGetsAGap(t *testing.T) {
	s := serving(t, 0, true)
	st := s.open(t, "0123456789abcdef-7")
	gap, _ := st.next(t)
	if gap.Type != "shop."+events.GapVerb || !strings.Contains(string(gap.Data), `"lastKnown":"0123456789abcdef-7"`) || strings.Contains(string(gap.Data), "oldestKept") {
		t.Fatalf("gap %+v", gap)
	}
}

func TestANewStreamGetsOnlyNewEvents(t *testing.T) {
	s := serving(t, 0, true)
	s.add(t, "item-1")
	st := s.open(t, "")
	s.add(t, "item-2")
	if e, _ := st.next(t); e.Subject != "item-2" {
		t.Fatalf("event %+v", e)
	}
}

// Errors and codes

func TestARouteCausingATypeNotInTheCatalogIsRefusedAtStart(t *testing.T) {
	list := features.New([]features.Feature{events.Feature}, (&switches{}).store())
	if err := events.Attach(list, events.Config{Product: "shop", Catalog: []string{"shop.item.added"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Handler(serve.Site{Routes: routes(), Features: list}, token, nil); !errors.Is(err, events.ErrNotInCatalog) {
		t.Fatalf("err = %v", err)
	}
	noSubject := []serve.Route{{Pattern: "POST /api/x", Action: serveCreate, Causes: serve.Cause{Type: "shop.item.added"}, Handler: func(http.ResponseWriter, *http.Request) {}}}
	if _, err := app.Handler(serve.Site{Routes: noSubject, Features: list}, token, nil); err == nil {
		t.Fatal("a route causing an event without its subject was taken")
	}
}

func TestAChangeThatFailsSendsNothing(t *testing.T) {
	s := serving(t, 0, true)
	st := s.open(t, "")
	if status := s.change(t, http.MethodDelete, "/api/items/missing", nil); status != http.StatusNotFound {
		t.Fatalf("status %d", status)
	}
	resp, cancel := s.do(t, http.MethodPost, "/api/items/item-0", map[string]string{app.TokenHeader(): token})
	resp.Body.Close()
	cancel()
	s.add(t, "item-1")
	if e, _ := st.next(t); e.Subject != "item-1" {
		t.Fatalf("event %+v: a change that failed or was not confirmed was sent", e)
	}
}

func TestAttachRefusesNamesThatBreakTheEventNames(t *testing.T) {
	for name, cfg := range map[string]events.Config{
		"another product's type":          {Product: "shop", Catalog: []string{"store.item.added"}},
		"a type of two parts":             {Product: "shop", Catalog: []string{"shop.added"}},
		"a type shorter than the product": {Product: "warehouse", Catalog: []string{"a.b.c"}},
		"a product not an ID":             {Product: "Shop"},
		"a thing with a slash":            {Product: "shop", Thing: "a/b"},
	} {
		if err := events.Attach(features.New([]features.Feature{events.Feature}, (&switches{}).store()), cfg); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
	if err := events.Attach(nil, events.Config{Product: "shop"}); err == nil {
		t.Error("taken without a feature list")
	}
	if err := events.Attach(features.New(nil, (&switches{}).store()), events.Config{Product: "shop"}); err == nil {
		t.Error("taken by a list without the events feature")
	}
}

func TestTheGuardRefusesAStreamWithoutTheToken(t *testing.T) {
	s := serving(t, 0, true)
	resp, cancel := s.do(t, http.MethodGet, events.Path, nil)
	defer cancel()
	defer resp.Body.Close()
	refusal(t, resp, http.StatusUnauthorized, serve.CodeToken)
}

func TestSwitchesThatCannotBeReadAreSaid(t *testing.T) {
	s := serving(t, 0, true)
	s.sw.failing(errors.New("config.yaml: cannot read"))
	resp, cancel := s.do(t, http.MethodGet, events.Path, map[string]string{app.TokenHeader(): token})
	defer cancel()
	defer resp.Body.Close()
	refusal(t, resp, http.StatusInternalServerError, serve.CodeConfig)
}

// The feature off

func TestTheFeatureOffServesNoStream(t *testing.T) {
	s := serving(t, 0, true)
	if err := s.list.Set(events.Feature.ID, false); err != nil {
		t.Fatal(err)
	}
	resp, cancel := s.do(t, http.MethodGet, events.Path, map[string]string{app.TokenHeader(): token})
	defer cancel()
	defer resp.Body.Close()
	refusal(t, resp, http.StatusNotFound, serve.CodeNotFound)
}

func TestTheFeatureOffKeepsNothing(t *testing.T) {
	s := serving(t, 0, true)
	live := s.open(t, "")
	s.add(t, "item-1")
	first := live.ids(t, 1)[0]
	if err := s.list.Set(events.Feature.ID, false); err != nil {
		t.Fatal(err)
	}
	s.add(t, "item-2")
	if err := s.list.Set(events.Feature.ID, true); err != nil {
		t.Fatal(err)
	}
	s.add(t, "item-3")
	st := s.open(t, first)
	if e, _ := st.next(t); e.Subject != "item-3" {
		t.Fatalf("event %+v, want only item-3: something was kept while off", e)
	}
}

func TestSwitchingTheFeatureOffEndsAnOpenStream(t *testing.T) {
	s := serving(t, 0, true)
	st := s.open(t, "")
	if err := s.list.Set(events.Feature.ID, false); err != nil {
		t.Fatal(err)
	}
	if !st.ended() {
		t.Fatal("the stream sent an event after the feature was switched off")
	}
}

// CLI and HTTP parity: none, since no command reads the stream; the decisions call for none.

// Leaks

func TestAClientThatLeavesLeavesNoGoroutine(t *testing.T) {
	s := serving(t, 0, true)
	before := runtime.NumGoroutine()
	var streams []*stream
	for range 5 {
		streams = append(streams, s.open(t, ""))
	}
	s.add(t, "item-1")
	for _, st := range streams {
		st.next(t)
		st.stop()
		st.resp.Body.Close()
	}
	http.DefaultClient.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("%d goroutines, %d before:\n%s", runtime.NumGoroutine(), before, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.add(t, "item-2")
}
