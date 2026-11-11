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

	"github.com/tugbayatilla/mankit/events"
	"github.com/tugbayatilla/mankit/features"
	"github.com/tugbayatilla/mankit/serve"
)

var app = serve.App{ID: "shop", Version: "1.0.0", Contracts: []string{"v1"}}

const token = "s3cret"

var catalog = []string{"shop.item.added", "shop.item.removed"}

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

type shop struct {
	broker *events.Broker
	list   *features.List
	sw     *switches
	url    string
}

// serving is a shop that serves its events behind serve's guard, as an app does.
func serving(t *testing.T, keep int) *shop {
	t.Helper()
	sw := &switches{}
	list := features.New([]features.Feature{events.Feature}, sw.store())
	b, err := events.New(events.Config{Product: "shop", Thing: "main", Catalog: catalog, Features: list, Heartbeat: 20 * time.Millisecond, Keep: keep})
	if err != nil {
		t.Fatal(err)
	}
	h, err := app.Handler(serve.Site{Routes: []serve.Route{b.Route()}}, token, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &shop{broker: b, list: list, sw: sw, url: srv.URL + events.Path}
}

// stream is an open GET /api/events.
type stream struct {
	resp *http.Response
	r    *bufio.Reader
	stop context.CancelFunc
}

func (s *shop) get(t *testing.T, header map[string]string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
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

func (s *shop) open(t *testing.T, lastID string) *stream {
	t.Helper()
	header := map[string]string{app.TokenHeader(): token}
	if lastID != "" {
		header["Last-Event-ID"] = lastID
	}
	resp, cancel := s.get(t, header)
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, Content-Type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return &stream{resp: resp, r: bufio.NewReader(resp.Body), stop: cancel}
}

// next is the next event on the stream, its id: line and its data: line; it skips heartbeat comments.
func (st *stream) next(t *testing.T) (string, []byte) {
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
			return id, []byte(data)
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

// ended says whether the stream ends without another event.
func (st *stream) ended(t *testing.T) bool {
	t.Helper()
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

func publish(t *testing.T, s *shop, typ, subject string) events.Event {
	t.Helper()
	e, err := s.broker.Publish(context.Background(), typ, subject, map[string]string{"name": subject})
	if err != nil {
		t.Fatal(err)
	}
	return e
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

func TestAnEventReachesEveryOpenStream(t *testing.T) {
	s := serving(t, 0)
	a, b := s.open(t, ""), s.open(t, "")
	e := publish(t, s, "shop.item.added", "item-1")
	for _, st := range []*stream{a, b} {
		id, data := st.next(t)
		var got events.Event
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if id != e.ID || got.ID != e.ID || got.Type != "shop.item.added" || got.Source != "/shop/main" || got.Subject != "item-1" || string(got.Data) != `{"name":"item-1"}` {
			t.Fatalf("id %q, event %+v", id, got)
		}
	}
}

func TestAnIdleStreamGetsAHeartbeat(t *testing.T) {
	s := serving(t, 0)
	st := s.open(t, "")
	line, err := st.r.ReadString('\n')
	if err != nil || line != ": heartbeat\n" {
		t.Fatalf("line %q, %v", line, err)
	}
}

func TestTheTraceOfTheRequestIsCarried(t *testing.T) {
	s := serving(t, 0)
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	e, err := s.broker.Publish(events.WithTrace(context.Background(), tp, "shop=1"), "shop.item.added", "item-1", nil)
	if err != nil || e.TraceParent != tp || e.TraceState != "shop=1" {
		t.Fatalf("event %+v, %v", e, err)
	}
	e, _ = s.broker.Publish(events.WithTrace(context.Background(), "00-"+strings.Repeat("0", 32)+"-00f067aa0ba902b7-01", "x"), "shop.item.added", "item-1", nil)
	if e.TraceParent == "" || strings.Contains(e.TraceParent, strings.Repeat("0", 32)) || e.TraceState != "" {
		t.Fatalf("an invalid traceparent was carried: %+v", e)
	}
}

// Contract

func TestEveryEventMeetsTheContract(t *testing.T) {
	s := serving(t, 0)
	st := s.open(t, "")
	publish(t, s, "shop.item.added", "item-1")
	if _, err := s.broker.Publish(events.WithTrace(context.Background(), "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "shop=1"), "shop.item.removed", "item-1", nil); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		_, data := st.next(t)
		if err := contracts.Validate("event/v1/event.schema.json", data); err != nil {
			t.Fatalf("%s: %v", data, err)
		}
	}
}

// Resume

func TestLastEventIDResumesAfterTheEventGiven(t *testing.T) {
	s := serving(t, 0)
	first := publish(t, s, "shop.item.added", "item-1")
	second := publish(t, s, "shop.item.added", "item-2")
	third := publish(t, s, "shop.item.removed", "item-1")
	st := s.open(t, first.ID)
	for _, want := range []string{second.ID, third.ID} {
		if id, _ := st.next(t); id != want {
			t.Fatalf("id %q, want %q", id, want)
		}
	}
	fourth := publish(t, s, "shop.item.removed", "item-2")
	if id, _ := st.next(t); id != fourth.ID {
		t.Fatalf("id %q, want the new event %q", id, fourth.ID)
	}
}

func TestALastEventIDNoLongerKeptGetsEveryEventKept(t *testing.T) {
	s := serving(t, 2)
	first := publish(t, s, "shop.item.added", "item-1")
	second := publish(t, s, "shop.item.added", "item-2")
	third := publish(t, s, "shop.item.added", "item-3")
	st := s.open(t, first.ID)
	for _, want := range []string{second.ID, third.ID} {
		if id, _ := st.next(t); id != want {
			t.Fatalf("id %q, want %q", id, want)
		}
	}
}

func TestANewStreamGetsOnlyNewEvents(t *testing.T) {
	s := serving(t, 0)
	publish(t, s, "shop.item.added", "item-1")
	st := s.open(t, "")
	e := publish(t, s, "shop.item.added", "item-2")
	if id, _ := st.next(t); id != e.ID {
		t.Fatalf("id %q, want %q", id, e.ID)
	}
}

// Errors and codes

func TestATypeNotInTheCatalogIsRefusedAndNotSent(t *testing.T) {
	s := serving(t, 0)
	if _, err := s.broker.Publish(context.Background(), "shop.item.sold", "item-1", nil); !errors.Is(err, events.ErrNotInCatalog) {
		t.Fatalf("err = %v", err)
	}
	st := s.open(t, "")
	e := publish(t, s, "shop.item.added", "item-1")
	if id, _ := st.next(t); id != e.ID {
		t.Fatalf("id %q: the refused event was sent", id)
	}
}

func TestAnEventWithoutItsSubjectOrWithDataNotJSONIsRefused(t *testing.T) {
	s := serving(t, 0)
	if _, err := s.broker.Publish(context.Background(), "shop.item.added", "", nil); err == nil {
		t.Fatal("an event without its subject was built")
	}
	if _, err := s.broker.Publish(context.Background(), "shop.item.added", "item-1", func() {}); err == nil {
		t.Fatal("an event with data not JSON was built")
	}
}

func TestNewRefusesNamesThatBreakTheEventNames(t *testing.T) {
	list := features.New([]features.Feature{events.Feature}, (&switches{}).store())
	for name, cfg := range map[string]events.Config{
		"another product's type":          {Product: "shop", Catalog: []string{"store.item.added"}, Features: list},
		"a type of two parts":             {Product: "shop", Catalog: []string{"shop.added"}, Features: list},
		"a type shorter than the product": {Product: "warehouse", Catalog: []string{"a.b.c"}, Features: list},
		"a product not an ID":             {Product: "Shop", Features: list},
		"a thing with a slash":            {Product: "shop", Thing: "a/b", Features: list},
		"no feature list":                 {Product: "shop"},
		"a list without events":           {Product: "shop", Features: features.New(nil, (&switches{}).store())},
	} {
		if _, err := events.New(cfg); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
}

func TestTheGuardRefusesAStreamWithoutTheToken(t *testing.T) {
	s := serving(t, 0)
	resp, cancel := s.get(t, nil)
	defer cancel()
	defer resp.Body.Close()
	refusal(t, resp, http.StatusUnauthorized, serve.CodeToken)
}

func TestSwitchesThatCannotBeReadAreSaid(t *testing.T) {
	s := serving(t, 0)
	s.sw.mu.Lock()
	s.sw.fail = errors.New("config.yaml: cannot read")
	s.sw.mu.Unlock()
	if _, err := s.broker.Publish(context.Background(), "shop.item.added", "item-1", nil); err == nil || !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("err = %v", err)
	}
	resp, cancel := s.get(t, map[string]string{app.TokenHeader(): token})
	defer cancel()
	defer resp.Body.Close()
	refusal(t, resp, http.StatusInternalServerError, serve.CodeConfig)
}

// The feature off

func TestTheFeatureOffServesNoStream(t *testing.T) {
	s := serving(t, 0)
	if err := s.list.Set(events.Feature.ID, false); err != nil {
		t.Fatal(err)
	}
	resp, cancel := s.get(t, map[string]string{app.TokenHeader(): token})
	defer cancel()
	defer resp.Body.Close()
	refusal(t, resp, http.StatusNotFound, serve.CodeNotFound)
}

func TestTheFeatureOffKeepsNothing(t *testing.T) {
	s := serving(t, 0)
	first := publish(t, s, "shop.item.added", "item-1")
	if err := s.list.Set(events.Feature.ID, false); err != nil {
		t.Fatal(err)
	}
	if e := publish(t, s, "shop.item.added", "item-2"); e.ID != "" {
		t.Fatalf("an event was built with the feature off: %+v", e)
	}
	if err := s.list.Set(events.Feature.ID, true); err != nil {
		t.Fatal(err)
	}
	third := publish(t, s, "shop.item.added", "item-3")
	st := s.open(t, first.ID)
	if id, _ := st.next(t); id != third.ID {
		t.Fatalf("id %q, want only %q: something was kept while off", id, third.ID)
	}
}

func TestSwitchingTheFeatureOffEndsAnOpenStream(t *testing.T) {
	s := serving(t, 0)
	st := s.open(t, "")
	if err := s.list.Set(events.Feature.ID, false); err != nil {
		t.Fatal(err)
	}
	if !st.ended(t) {
		t.Fatal("the stream sent an event after the feature was switched off")
	}
}

// CLI and HTTP parity: none, since no command reads the stream; the decisions call for none.

// Leaks

func TestAClientThatLeavesLeavesNoGoroutine(t *testing.T) {
	s := serving(t, 0)
	before := runtime.NumGoroutine()
	var streams []*stream
	for range 5 {
		streams = append(streams, s.open(t, ""))
	}
	publish(t, s, "shop.item.added", "item-1")
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
	publish(t, s, "shop.item.added", "item-2")
}
