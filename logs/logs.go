// Package logs is what every app logs (G-STACK-8, ADR-E5): records in OpenTelemetry's log shape, one JSON object a line, through log/slog.
// A foreground server logs to stdout, every other command to stderr, and a detached server to its file in the app's logs folder, rotated; sending them over OTLP is the telemetry feature, off by default (ADR-E9).
// A Problem in a record is an attribute in its own shape (contracts problem/v1), so a reader of the log decides by its type and reason as a reader of an answer does.
package logs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"go.naywee.com/mankit/v5/problem"
)

// Record is one log record in OpenTelemetry's log data model, the fields a local reader and an OTLP collector both know.
type Record struct {
	Timestamp      string            `json:"timestamp"`
	SeverityText   string            `json:"severityText"`
	SeverityNumber int               `json:"severityNumber"`
	Body           string            `json:"body"`
	Attributes     map[string]any    `json:"attributes,omitempty"`
	Resource       map[string]string `json:"resource"`
}

// New is a logger writing records to w for the app id at version, at Info and above; more handlers, such as the telemetry feature's, get every record too.
// A person at a terminal reads each record as one plain line, "<id>: <body>", with a Problem's detail after it; anything else, a pipe, a file or a container's log, gets the JSON lines.
func New(w io.Writer, id, version string, more ...slog.Handler) *slog.Logger {
	var h slog.Handler = &handler{out: &out{w: w}, level: slog.LevelInfo, human: terminal(w),
		resource: map[string]string{"service.name": id, "service.version": version}}
	if len(more) > 0 {
		h = fan(append([]slog.Handler{h}, more...))
	}
	return slog.New(h)
}

// out is a writer shared by a handler and every one derived from it, written one record at a time.
type out struct {
	mu sync.Mutex
	w  io.Writer
}

type handler struct {
	out      *out
	human    bool // one plain line a record, for a person at a terminal
	level    slog.Leveler
	resource map[string]string
	attrs    []slog.Attr
	group    string
}

func (h *handler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level.Level() }

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]any{}
	for _, a := range h.attrs {
		put(attrs, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		put(attrs, h.group, a)
		return true
	})
	text, number := severity(r.Level)
	at := r.Time
	if at.IsZero() {
		at = time.Now()
	}
	rec := Record{Timestamp: at.UTC().Format(time.RFC3339Nano), SeverityText: text, SeverityNumber: number,
		Body: r.Message, Resource: h.resource}
	if len(attrs) > 0 {
		rec.Attributes = attrs
	}
	var line []byte
	var err error
	if h.human {
		line = []byte(plain(rec))
	} else if line, err = json.Marshal(rec); err != nil {
		return err
	}
	h.out.mu.Lock()
	defer h.out.mu.Unlock()
	_, err = h.out.w.Write(append(line, '\n'))
	return err
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append([]slog.Attr{}, h.attrs...)
	for _, a := range as {
		if h.group != "" {
			a.Key = h.group + "." + a.Key
		}
		c.attrs = append(c.attrs, a)
	}
	return &c
}

func (h *handler) WithGroup(name string) slog.Handler {
	c := *h
	c.group = strings.TrimPrefix(h.group+"."+name, ".")
	return &c
}

// plain is rec as a person reads it: "<service>: <body>", warnings and errors marked, then the detail of each Problem it carries.
func plain(rec Record) string {
	var b strings.Builder
	b.WriteString(rec.Resource["service.name"] + ": ")
	switch rec.SeverityText {
	case "WARN":
		b.WriteString("warning: ")
	case "ERROR":
		b.WriteString("error: ")
	}
	b.WriteString(rec.Body)
	for _, v := range rec.Attributes {
		if p, ok := v.(problem.Problem); ok {
			b.WriteString("; " + p.Detail)
		}
	}
	return b.String()
}

// Terminal says whether stream is a terminal a person reads: the one place mankit asks, for a log's plain lines, the token link that goes to a person only, and a question asked (ask).
func Terminal(stream any) bool {
	f, ok := stream.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// terminal is Terminal for the handler; a var only so a test can stand in for one.
var terminal = func(w io.Writer) bool { return Terminal(w) }

// put adds a to attrs as OpenTelemetry keeps attributes: flat, a group's keys joined by dots; a Problem is its own shape, another error its text.
func put(attrs map[string]any, group string, a slog.Attr) {
	key := a.Key
	if group != "" {
		key = group + "." + key
	}
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindGroup:
		for _, g := range v.Group() {
			put(attrs, key, g)
		}
		return
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			var p *problem.Problem
			if errors.As(err, &p) {
				shaped := *p
				shaped.APIVersion, shaped.Kind = problem.APIVersion, problem.Kind
				attrs[key] = shaped
			} else {
				attrs[key] = err.Error()
			}
			return
		}
	}
	if key != "" {
		attrs[key] = v.Any()
	}
}

// severity is l in OpenTelemetry's words and numbers: DEBUG 5, INFO 9, WARN 13, ERROR 17, a level between them the number past its own.
func severity(l slog.Level) (string, int) {
	switch {
	case l < slog.LevelInfo:
		return "DEBUG", 5 + int(l-slog.LevelDebug)
	case l < slog.LevelWarn:
		return "INFO", 9 + int(l-slog.LevelInfo)
	case l < slog.LevelError:
		return "WARN", 13 + int(l-slog.LevelWarn)
	}
	return "ERROR", min(17+int(l-slog.LevelError), 20)
}

// fan hands every record to each handler.
type fan []slog.Handler

func (f fan) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fan) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (f fan) WithAttrs(as []slog.Attr) slog.Handler {
	out := make(fan, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(as)
	}
	return out
}

func (f fan) WithGroup(name string) slog.Handler {
	out := make(fan, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}

// Writer is an io.Writer that logs each line written to it as one record at level, for what reports through a writer, such as store.App.Log and serve's Site.Log.
func Writer(l *slog.Logger, level slog.Level) io.Writer { return &lines{l: l, level: level} }

type lines struct {
	mu      sync.Mutex
	l       *slog.Logger
	level   slog.Level
	pending []byte
}

func (w *lines) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	for {
		i := strings.IndexByte(string(w.pending), '\n')
		if i < 0 {
			return len(p), nil
		}
		if line := strings.TrimSpace(string(w.pending[:i])); line != "" {
			w.l.Log(context.Background(), w.level, line)
		}
		w.pending = w.pending[i+1:]
	}
}
