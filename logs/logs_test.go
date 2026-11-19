package logs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tugbayatilla/mankit/v4/features"
	"github.com/tugbayatilla/mankit/v4/logs"
	"github.com/tugbayatilla/mankit/v4/problem"
)

func records(t *testing.T, b *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("%q is no JSON record: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

func TestARecordIsInOpenTelemetrysLogShape(t *testing.T) {
	var b bytes.Buffer
	l := logs.New(&b, "designer", "9.0.0")
	l.With("addr", "127.0.0.1:7480").WithGroup("http").Warn("slow answer", "ms", 1200)
	r := records(t, &b)[0]
	if r["severityText"] != "WARN" || r["severityNumber"] != float64(13) || r["body"] != "slow answer" {
		t.Errorf("severity and body: %v", r)
	}
	if _, ok := r["timestamp"].(string); !ok {
		t.Errorf("no timestamp: %v", r)
	}
	attrs := r["attributes"].(map[string]any)
	if attrs["addr"] != "127.0.0.1:7480" || attrs["http.ms"] != float64(1200) {
		t.Errorf("attributes, flat and dotted: %v", attrs)
	}
	res := r["resource"].(map[string]any)
	if res["service.name"] != "designer" || res["service.version"] != "9.0.0" {
		t.Errorf("resource: %v", res)
	}
}

func TestEachLevelHasItsSeverityAndDebugIsLeftOut(t *testing.T) {
	var b bytes.Buffer
	l := logs.New(&b, "designer", "9.0.0")
	l.Debug("not kept")
	l.Info("i")
	l.Error("e")
	l.Log(context.Background(), slog.LevelError+2, "e2")
	got := records(t, &b)
	var words []string
	for _, r := range got {
		words = append(words, fmt.Sprint(r["severityText"], r["severityNumber"]))
	}
	if strings.Join(words, " ") != "INFO9 ERROR17 ERROR19" {
		t.Errorf("severities %v", words)
	}
}

// A Problem in a record is in its own shape (problem/v1), so a reader decides by its type and reason; another error is its text.
func TestAProblemIsAnAttributeInItsOwnShape(t *testing.T) {
	var b bytes.Buffer
	l := logs.New(&b, "designer", "9.0.0")
	p := problem.FailAt(problem.TypeFailed, "feature_needs_values", "telemetry", "the feature telemetry needs values that are not set")
	l.Error("a feature cannot run", "problem", fmt.Errorf("start: %w", p), "cause", errors.New("plain"))
	attrs := records(t, &b)[0]["attributes"].(map[string]any)
	got, ok := attrs["problem"].(map[string]any)
	if !ok || got["type"] != "failed" || got["reason"] != "feature_needs_values" || got["detail"] == "" {
		t.Errorf("problem: %v", attrs["problem"])
	}
	if attrs["cause"] != "plain" {
		t.Errorf("cause: %v", attrs["cause"])
	}
}

func TestAWriterLogsEachLine(t *testing.T) {
	var b bytes.Buffer
	w := logs.Writer(logs.New(&b, "designer", "9.0.0"), slog.LevelWarn)
	fmt.Fprint(w, "store: one\nstore: tw")
	fmt.Fprint(w, "o\n\n")
	got := records(t, &b)
	if len(got) != 2 || got[0]["body"] != "store: one" || got[1]["body"] != "store: two" || got[1]["severityText"] != "WARN" {
		t.Errorf("records %v", got)
	}
}

// A log file is rotated by size and keeps its five newest before it (G-STACK-8), each this user's alone.
func TestALogFileRotatesAndKeepsFive(t *testing.T) {
	defer logs.Rotation(100, 5)()
	path := filepath.Join(t.TempDir(), "logs", "127.0.0.1-7480.log")
	f, err := logs.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := 0; i < 20; i++ {
		if _, err := fmt.Fprintf(f, "%02d %s\n", i, strings.Repeat("x", 40)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 5; i++ {
		if _, err := os.Stat(fmt.Sprintf("%s.%d", path, i)); err != nil {
			t.Errorf("no %s.%d: %v", path, i, err)
		}
	}
	if _, err := os.Stat(path + ".6"); !os.IsNotExist(err) {
		t.Errorf("a sixth old file is kept")
	}
	now, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(now), "18 ") || len(now) > 100 {
		t.Errorf("the file now holds %q", now)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	if info, _ := os.Stat(filepath.Dir(path)); info.Mode().Perm() != 0o700 {
		t.Errorf("folder mode %v", info.Mode().Perm())
	}
}

// The telemetry feature sends nothing off, or on without its endpoint, and sends once both are there (ADR-E5, E9).
func TestTelemetrySendsOnlyWhenOnWithItsEndpoint(t *testing.T) {
	ctx := context.Background()
	if h, _, err := logs.Export(ctx, nil, "designer", "9.0.0"); h != nil || err != nil {
		t.Fatalf("no list: %v %v", h, err)
	}
	env := map[string]bool{}
	list := func(on bool) *features.List {
		ids := func() ([]string, error) {
			if on {
				return []string{logs.Telemetry.ID}, nil
			}
			return nil, nil
		}
		store := features.Store{Off: func() ([]string, error) { return nil, nil }, Keep: func(string, bool) error { return nil }, On: ids}
		return features.New([]features.Feature{logs.Telemetry}, store).WithLookup(func(name string) bool { return env[name] })
	}
	if h, _, err := logs.Export(ctx, list(false), "designer", "9.0.0"); h != nil || err != nil {
		t.Fatalf("off: %v %v", h, err)
	}
	if h, _, err := logs.Export(ctx, list(true), "designer", "9.0.0"); h != nil || err != nil {
		t.Fatalf("on without the endpoint: %v %v", h, err)
	}
	broken, _ := list(true).Broken()
	if len(broken) != 1 || broken[0].Missing[0].Name != logs.Endpoint {
		t.Fatalf("on without the endpoint is not said: %+v", broken)
	}
	env[logs.Endpoint] = true
	t.Setenv(logs.Endpoint, "http://127.0.0.1:4318")
	h, stop, err := logs.Export(ctx, list(true), "designer", "9.0.0")
	if h == nil || err != nil {
		t.Fatalf("on with the endpoint: %v %v", h, err)
	}
	_ = stop(ctx)
}

// A person at a terminal reads one plain line a record, never JSON.
func TestAtATerminalARecordIsOnePlainLine(t *testing.T) {
	logs.AsTerminal(t)
	var b bytes.Buffer
	l := logs.New(&b, "designer", "9.0.0")
	l.Info("serving on 127.0.0.1:5174", "url", "http://127.0.0.1:5174")
	l.Warn("a feature is on but cannot run; the rest serves", "problem", problem.Fail(problem.TypeFailed, "feature_needs_values", "the feature hear needs HEAR_URL"))
	want := "designer: serving on 127.0.0.1:5174\ndesigner: warning: a feature is on but cannot run; the rest serves; the feature hear needs HEAR_URL\n"
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}
