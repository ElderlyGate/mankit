// Package queue is the change queue every app shares (ADR-S14): one change at a time per served thing, across processes, done once written and read back.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tugbayatilla/mankit/home"
	"github.com/tugbayatilla/mankit/serve"
)

// The codes of the errors the queue gives; a program decides by the code, never by the message (G-STACK-15).
const (
	CodeTimeout     = "queue_timeout"  // the change waited longer than Queue.Wait for its turn and did not run
	CodeNotReadBack = "not_read_back"  // the change ran, but what it wrote could not be read back
	CodeLost        = "ticket_lost"    // another process took the waiting ticket for stale and moved it to bad/
	CodeUnusable    = "queue_unusable" // the queue's folder cannot be made, read or written

	CodeBackendUnsupported = "queue_backend_unsupported" // .queue/backend names a backend this build does not run, or cannot be read
	CodeTooLong            = "change_too_long"           // the change held the turn past the run limit; its turn ended once write returned
)

// DefaultWait is how long a change waits for its turn when the app sets no Wait.
const DefaultWait = 30 * time.Second

// Kind is the kind of a ticket, and ListKind the kind of the answer <app> queue gives (contracts mankit/v1).
const (
	Kind     = "Ticket"
	ListKind = "QueueList"
)

// BackendKind is the kind of .queue/backend, the marker naming the backend every writer of the folder uses (contracts mankit/v1).
const BackendKind = "QueueBackend"

// The backends a marker may name; this build runs files alone.
const (
	BackendFiles       = "files"
	BackendLockService = "lock-service"
)

// The names in the queue's folder; every app version sharing a queue relies on them (contracts mankit/v1).
const (
	folderName  = ".queue"
	badName     = "bad"
	lockName    = "lock"
	turnName    = "turn"
	ignoreName  = ".gitignore"
	backendName = "backend"
)

// staleAfter and touchEvery are fixed by contracts mankit/v1, so every app sharing a queue agrees; vars only so tests can shorten them.
var (
	staleAfter = 30 * time.Second
	touchEvery = 5 * time.Second
	runLimit   = 2 * time.Minute // how long a change may hold the turn; write and readBack get a context that ends then
)

const (
	firstPause = 5 * time.Millisecond
	lastPause  = 200 * time.Millisecond
	leaveFor   = 5 * time.Second
)

var idPattern = regexp.MustCompile(`^[0-9]{20}$`)

// APIVersion is the ticket's apiVersion; two app versions share a queue at once, so it never changes (ADR-S15).
func APIVersion() string { return home.Org + "/mankit/ticket/v1" }

// BackendAPIVersion is the apiVersion of .queue/backend; it never changes (ADR-S15).
func BackendAPIVersion() string { return home.Org + "/mankit/queue-backend/v1" }

// Queue is the queue of one served thing, as one app sees it.
type Queue struct {
	Folder  string        // the served thing's app folder, <served folder>/<app folder>; the queue is its .queue
	App     string        // the product ID of the app that waits
	Version string        // the app's version, written on its tickets
	Wait    time.Duration // how long a change waits for its turn; zero is DefaultWait
	Log     io.Writer     // where a ticket set aside or a failed leave is said; nil is stderr
}

// Ticket is one change waiting its turn, as written in the queue's folder (contracts mankit/v1 ticket).
type Ticket struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Metadata   Metadata `json:"metadata"`
	Spec       Spec     `json:"spec"`
}

// Metadata is the ticket's fixed header: never renamed or removed (ADR-S15).
type Metadata struct {
	ID   string `json:"id"` // 20 digits: its place in the line and its file name; a lower one goes first
	App  string `json:"app"`
	PID  int    `json:"pid"`
	Host string `json:"host"`
}

// Spec is what the ticket says about its change; a newer app may add fields.
type Spec struct {
	Change   string    `json:"change"`
	JoinedAt time.Time `json:"joinedAt"`
	Version  string    `json:"version,omitempty"`
}

// Listing is what <app> queue shows: the tickets in the order they go, and the files moved to bad/.
type Listing struct {
	Tickets []Place `json:"tickets"`
	Bad     []Bad   `json:"bad"`
}

// Place is a ticket in the line, whether its change runs now, and since when.
type Place struct {
	Ticket Ticket     `json:"ticket"`
	Turn   bool       `json:"turn"`
	Since  *time.Time `json:"since,omitempty"` // when the change that runs now took the turn
}

// Bad is a file moved to .queue/bad/, by its name there.
type Bad struct {
	File string `json:"file"`
}

// Change runs write once every change queued before it on this thing is done, then reads it back; the change is done only once readBack is.
// write and readBack get a context that ends at the run limit (2 minutes) and must stop when it does: the turn is held until write returns.
// An error from write is given back as it is; the queue's own errors carry its codes.
func (q Queue) Change(ctx context.Context, change string, write, readBack func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := q.backend()
	if err != nil {
		return err
	}
	return b.change(ctx, q, change, write, readBack)
}

// List is the queue now, from the backend the folder names.
func (q Queue) List(ctx context.Context) (Listing, error) {
	b, err := q.backend()
	if err != nil {
		return Listing{}, err
	}
	return b.list(ctx, q)
}

// backend is how a queue takes turns; the folder's marker picks one for every writer, so two apps never take turns two ways.
type backend interface {
	change(ctx context.Context, q Queue, change string, write, readBack func(context.Context) error) error
	list(ctx context.Context, q Queue) (Listing, error)
}

// backendMarker is .queue/backend; it never holds an address or a token, which come from the app's settings.
type backendMarker struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		Type string `json:"type"`
	} `json:"spec"`
}

// backend is the one the folder's marker names, files without one; a marker it cannot read or run is refused, never taken for files.
func (q Queue) backend() (backend, error) {
	// A folder that cannot be a folder is the queue's own problem, not the marker's.
	if info, err := os.Stat(q.dir()); errors.Is(err, os.ErrNotExist) {
		return files{}, nil
	} else if err != nil || !info.IsDir() {
		return nil, unusable("cannot use %s as its folder: %v", q.dir(), err)
	}
	path := filepath.Join(q.dir(), backendName)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return files{}, nil
	}
	refuse := func(why string) error {
		return serve.Fail(CodeBackendUnsupported, fmt.Sprintf("the change queue's marker %s %s; no change is made here until it names %q", path, why, BackendFiles))
	}
	if err != nil {
		return nil, refuse(fmt.Sprintf("cannot be read: %v", err))
	}
	var m backendMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, refuse(fmt.Sprintf("is not JSON: %v", err))
	}
	if m.APIVersion != BackendAPIVersion() || m.Kind != BackendKind {
		return nil, refuse(fmt.Sprintf("is not a %s %s", BackendAPIVersion(), BackendKind))
	}
	if m.Spec.Type != BackendFiles {
		return nil, refuse(fmt.Sprintf("names the backend %q, which this build does not run", m.Spec.Type))
	}
	return files{}, nil
}

// files takes turns with files and their locks in the queue's folder (contracts README, The change queue on disk).
type files struct{}

func (files) change(ctx context.Context, q Queue, change string, write, readBack func(context.Context) error) error {
	waiting, cancel := context.WithTimeout(ctx, q.wait())
	defer cancel()
	t, err := q.join(waiting, change)
	if err != nil {
		return q.gaveUp(ctx, err)
	}
	defer t.leave()
	if err := t.await(waiting); err != nil {
		return q.gaveUp(ctx, err)
	}
	running, stop := context.WithTimeout(ctx, runLimit)
	defer stop()
	tooLong := func() error {
		if errors.Is(running.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return serve.Fail(CodeTooLong, fmt.Sprintf("%s held the turn in %s past the limit of %s and is not done", change, q.dir(), runLimit))
		}
		return nil
	}
	err = write(running)
	if late := tooLong(); late != nil {
		return late
	}
	if err != nil {
		return err
	}
	err = readBack(running)
	if late := tooLong(); late != nil {
		return late
	}
	if err != nil {
		return serve.Fail(CodeNotReadBack, fmt.Sprintf("%s was written but could not be read back: %v", change, err))
	}
	return nil
}

// list is the queue now; tickets of dead processes go and bad ones are set aside first, as every reader of the queue does.
func (files) list(ctx context.Context, q Queue) (Listing, error) {
	out := Listing{Tickets: []Place{}, Bad: []Bad{}}
	if _, err := os.Stat(q.dir()); errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	err := q.locked(ctx, func() error {
		tickets, err := q.sweep()
		if err != nil {
			return err
		}
		h, err := q.turnHolder()
		if err != nil {
			return err
		}
		for _, t := range tickets {
			place := Place{Ticket: t, Turn: h.ID != "" && t.Metadata.ID == h.ID}
			if place.Turn && !h.Since.IsZero() {
				place.Since = &h.Since
			}
			out.Tickets = append(out.Tickets, place)
		}
		bad, err := os.ReadDir(filepath.Join(q.dir(), badName))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return unusable("cannot read %s: %v", filepath.Join(q.dir(), badName), err)
		}
		for _, e := range bad {
			out.Bad = append(out.Bad, Bad{File: e.Name()})
		}
		return nil
	})
	return out, err
}

func (q Queue) dir() string { return filepath.Join(q.Folder, folderName) }

func (q Queue) wait() time.Duration {
	if q.Wait > 0 {
		return q.Wait
	}
	return DefaultWait
}

func (q Queue) log() io.Writer {
	if q.Log != nil {
		return q.Log
	}
	return os.Stderr
}

// gaveUp is the error of a wait that ended: the queue's timeout when its own deadline passed, else what the caller's context says.
func (q Queue) gaveUp(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return serve.Fail(CodeTimeout, fmt.Sprintf("the change waited %s for its turn in %s and did not run%s", q.wait(), q.dir(), q.holderSays()))
	}
	return err
}

// holderSays names the change that holds the turn, for a person deciding whether to wait or stop it; empty when none can be named.
func (q Queue) holderSays() string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	says := ""
	_ = q.locked(ctx, func() error {
		h, err := q.turnHolder()
		if err != nil || h.ID == "" {
			return err
		}
		says = "; ticket " + h.ID + " holds the turn"
		path := filepath.Join(q.dir(), h.ID+".json")
		if info, err := os.Lstat(path); err == nil {
			if t, why := read(path, fs.FileInfoToDirEntry(info)); why == "" {
				says += fmt.Sprintf(" (pid %d on %s)", t.Metadata.PID, t.Metadata.Host)
			}
		}
		if !h.Since.IsZero() {
			says += fmt.Sprintf(" and has held it for %s", time.Since(h.Since).Round(time.Millisecond))
		}
		return nil
	})
	return says
}

func unusable(format string, args ...any) error {
	return serve.Fail(CodeUnusable, "the change queue "+fmt.Sprintf(format, args...))
}

// retry calls try until it succeeds, fails or ctx ends, pausing longer each time up to lastPause, so a long wait reads the folder seldom.
func retry(ctx context.Context, try func() (bool, error)) error {
	pause := firstPause
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, err := try()
		if err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pause):
		}
		pause = min(pause*2, lastPause)
	}
}

// inProcess holds one process's own locks on a queue folder, so changes in one process take turns without leaning on the file system's locks.
type inProcess struct {
	folder chan struct{} // held by sending, so a goroutine waits for it in line and gives up when its context ends
	turn   sync.Mutex
	mu     sync.Mutex
	mine   map[string]bool // the ticket files this process holds, which a sweep here never takes for dead
}

var processes sync.Map

func (q Queue) process() *inProcess {
	key, err := filepath.Abs(q.dir())
	if err != nil {
		key = filepath.Clean(q.dir())
	}
	p, _ := processes.LoadOrStore(key, &inProcess{folder: make(chan struct{}, 1), mine: map[string]bool{}})
	return p.(*inProcess)
}

func (p *inProcess) holds(path string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mine[path]
}

func (p *inProcess) hold(path string, on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if on {
		p.mine[path] = true
	} else {
		delete(p.mine, path)
	}
}

// ticket is this process's place in the line while its change waits and runs; its file stays locked for as long, which says it is alive.
type ticket struct {
	q     Queue
	id    string
	path  string
	alive *os.File
	turn  *os.File
	stop  chan struct{}
	done  sync.WaitGroup
}

// join writes a ticket at the end of the line and holds its lock until it leaves.
func (q Queue) join(ctx context.Context, change string) (*ticket, error) {
	if err := q.prepare(ctx); err != nil {
		return nil, err
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	t := &ticket{q: q, stop: make(chan struct{})}
	err = q.locked(ctx, func() error {
		tickets, err := q.sweep()
		if err != nil {
			return err
		}
		t.id = nextID(tickets, time.Now())
		t.path = filepath.Join(q.dir(), t.id+".json")
		data, err := json.MarshalIndent(Ticket{
			APIVersion: APIVersion(),
			Kind:       Kind,
			Metadata:   Metadata{ID: t.id, App: q.App, PID: os.Getpid(), Host: host},
			Spec:       Spec{Change: change, JoinedAt: time.Now().UTC(), Version: q.Version},
		}, "", "  ")
		if err != nil {
			return err
		}
		if err := writeWhole(q.dir(), t.path, append(data, '\n')); err != nil {
			return unusable("cannot write a ticket in %s: %v", q.dir(), err)
		}
		// Locked before the folder's lock is given up, so no sweep ever sees this ticket without its owner.
		f, err := os.Open(t.path)
		if err == nil {
			var got bool
			if got, err = lockFile(f); err == nil && !got {
				err = errors.New("another process holds it")
			}
			if err != nil {
				f.Close()
			}
		}
		if err != nil {
			_ = os.Remove(t.path)
			return unusable("cannot lock the ticket %s: %v", t.path, err)
		}
		t.alive = f
		q.process().hold(t.path, true)
		return nil
	})
	if err != nil {
		return nil, err
	}
	t.done.Add(1)
	go t.touch()
	return t, nil
}

// nextID is after every ticket in the line, and close to now so a person reading the folder sees the order.
func nextID(tickets []Ticket, now time.Time) string {
	next := now.UnixNano()
	for _, t := range tickets {
		if n, err := strconv.ParseInt(t.Metadata.ID, 10, 64); err == nil && n >= next {
			next = n + 1
		}
	}
	return fmt.Sprintf("%020d", next)
}

// touch keeps the ticket's time fresh, for a reader that cannot tell from the lock whether its owner still runs.
func (t *ticket) touch() {
	defer t.done.Done()
	tick := time.NewTicker(touchEvery)
	defer tick.Stop()
	for {
		select {
		case <-t.stop:
			return
		case now := <-tick.C:
			_ = os.Chtimes(t.path, now, now)
		}
	}
}

// await returns once the ticket is first in the line and holds the turn, or when ctx ends.
func (t *ticket) await(ctx context.Context) error {
	return retry(ctx, func() (bool, error) {
		ready := false
		err := t.q.locked(ctx, func() error {
			var err error
			ready, err = t.try()
			return err
		})
		return ready, err
	})
}

// try takes the turn when the ticket is first; the turn's lock also keeps out a change whose ticket was set aside while it still runs.
func (t *ticket) try() (bool, error) {
	tickets, err := t.q.sweep()
	if err != nil {
		return false, err
	}
	if !holds(tickets, t.id) {
		return false, serve.Fail(CodeLost, fmt.Sprintf("the ticket %s was taken for stale and moved to %s; the change did not run", t.path, filepath.Join(t.q.dir(), badName)))
	}
	if tickets[0].Metadata.ID != t.id {
		return false, nil
	}
	p := t.q.process()
	if !p.turn.TryLock() {
		return false, nil
	}
	path := filepath.Join(t.q.dir(), turnName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		p.turn.Unlock()
		return false, unusable("cannot open %s: %v", path, err)
	}
	got, err := lockFile(f)
	if err != nil || !got {
		f.Close()
		p.turn.Unlock()
		if err != nil {
			return false, unusable("cannot lock %s: %v", path, err)
		}
		return false, nil
	}
	t.turn = f
	if err := say(f, t.id+"\n"+time.Now().UTC().Format(time.RFC3339Nano)+"\n"); err != nil {
		return false, unusable("cannot write %s: %v", path, err)
	}
	return true, nil
}

// say makes f hold text alone.
func say(f *os.File, text string) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err := f.WriteAt([]byte(text), 0)
	return err
}

func holds(tickets []Ticket, id string) bool {
	for _, t := range tickets {
		if t.Metadata.ID == id {
			return true
		}
	}
	return false
}

// leave gives the turn up and takes the ticket out of the line, whether the change ran or not; it is not cut short by the caller's context.
func (t *ticket) leave() {
	close(t.stop)
	t.done.Wait()
	p := t.q.process()
	if t.turn != nil {
		if err := say(t.turn, ""); err != nil {
			fmt.Fprintf(t.q.log(), "queue: cannot clear %s: %v\n", t.turn.Name(), err)
		}
		_ = unlockFile(t.turn)
		t.turn.Close()
		p.turn.Unlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), leaveFor)
	defer cancel()
	err := t.q.locked(ctx, func() error {
		_ = unlockFile(t.alive)
		t.alive.Close()
		p.hold(t.path, false)
		err := os.Remove(t.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	})
	if err != nil {
		fmt.Fprintf(t.q.log(), "queue: cannot take the ticket %s out of the line: %v\n", t.path, err)
	}
}

// prepare makes the queue's folder only this user may enter, and keeps it out of git without touching the served folder's own files.
func (q Queue) prepare(ctx context.Context) error {
	dir := q.dir()
	for _, d := range []string{dir, filepath.Join(dir, badName)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return unusable("cannot make %s: %v", d, err)
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return unusable("cannot keep %s to this user: %v", d, err)
		}
	}
	return q.locked(ctx, func() error {
		ignore := filepath.Join(dir, ignoreName)
		if _, err := os.Stat(ignore); errors.Is(err, os.ErrNotExist) {
			if err := writeWhole(dir, ignore, []byte("*\n")); err != nil {
				return unusable("cannot write %s: %v", ignore, err)
			}
		}
		return nil
	})
}

// locked runs fn while this process alone may change the queue's folder, waiting for it until ctx ends.
func (q Queue) locked(ctx context.Context, fn func() error) error {
	p := q.process()
	select {
	case p.folder <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-p.folder }()
	path := filepath.Join(q.dir(), lockName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return unusable("cannot open %s: %v", path, err)
	}
	defer f.Close()
	if err := retry(ctx, func() (bool, error) { return lockFile(f) }); err != nil {
		if ctx.Err() != nil {
			return err
		}
		return unusable("cannot lock %s: %v", path, err)
	}
	defer unlockFile(f)
	return fn()
}

// holder is the ticket whose change runs now and when it took the turn, as its process wrote them in turn.
type holder struct {
	ID    string
	Since time.Time
}

// turnHolder is the change that runs now, or none; asked under the folder's lock, so trying the turn here holds up no one.
func (q Queue) turnHolder() (holder, error) {
	path := filepath.Join(q.dir(), turnName)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return holder{}, nil
	}
	if err != nil {
		return holder{}, unusable("cannot open %s: %v", path, err)
	}
	defer f.Close()
	p := q.process()
	if p.turn.TryLock() {
		defer p.turn.Unlock()
		free, err := lockFile(f)
		if err != nil {
			return holder{}, unusable("cannot lock %s: %v", path, err)
		}
		if free {
			_ = unlockFile(f)
			return holder{}, nil
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return holder{}, unusable("cannot read %s: %v", path, err)
	}
	// A turn written by a process that died half way names no one, rather than a wrong one.
	id, since, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
	if !idPattern.MatchString(id) {
		return holder{}, nil
	}
	h := holder{ID: id}
	if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(since)); err == nil {
		h.Since = t
	}
	return h, nil
}

// sweep is the line in order: a ticket whose process is gone is removed, and one that cannot be read or shows no sign of life is set aside in bad/ (G-CODE-6).
func (q Queue) sweep() ([]Ticket, error) {
	dir := q.dir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, unusable("cannot read %s: %v", dir, err)
	}
	p := q.process()
	var tickets []Ticket
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == badName || name == lockName || name == turnName || name == backendName {
			continue
		}
		path := filepath.Join(dir, name)
		t, why := read(path, e)
		if why == "" && !p.holds(path) {
			gone, err := q.ownerGone(path)
			if err != nil {
				return nil, err
			}
			if gone {
				continue
			}
			why = stale(e)
		}
		if why != "" {
			if err := q.moveBad(path, why); err != nil {
				return nil, err
			}
			continue
		}
		tickets = append(tickets, t)
	}
	sort.Slice(tickets, func(i, j int) bool { return tickets[i].Metadata.ID < tickets[j].Metadata.ID })
	return tickets, nil
}

// ownerGone removes the ticket at path when its lock is free: the system gave it back, so the process that held it has ended.
func (q Queue) ownerGone(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, nil
	}
	defer f.Close()
	free, err := lockFile(f)
	if err != nil || !free {
		return false, nil
	}
	defer unlockFile(f)
	if err := os.Remove(path); err != nil {
		return false, unusable("cannot remove %s, whose process has ended: %v", path, err)
	}
	fmt.Fprintf(q.log(), "queue: %s: its process has ended; removed\n", path)
	return true, nil
}

// stale is why a ticket whose owner still holds its lock is set aside anyway: no sign of life for staleAfter.
func stale(e os.DirEntry) string {
	info, err := e.Info()
	if err != nil {
		return fmt.Sprintf("cannot be read: %v", err)
	}
	if age := time.Since(info.ModTime()); age > staleAfter {
		return fmt.Sprintf("is stale: no sign of life for %s", age.Round(time.Second))
	}
	return ""
}

// read is the ticket at path, or why it is bad.
func read(path string, e os.DirEntry) (Ticket, string) {
	var t Ticket
	if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
		return t, "is not a ticket file"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return t, fmt.Sprintf("cannot be read: %v", err)
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return t, fmt.Sprintf("is not JSON: %v", err)
	}
	m, s := t.Metadata, t.Spec
	if t.APIVersion != APIVersion() || t.Kind != Kind || !idPattern.MatchString(m.ID) || m.ID+".json" != e.Name() ||
		m.App == "" || m.PID < 1 || m.Host == "" || s.Change == "" || s.JoinedAt.IsZero() {
		return t, fmt.Sprintf("is not a %s %s whose 20-digit id is its file name, with an app, a pid, a host, a change and when it joined", APIVersion(), Kind)
	}
	return t, ""
}

// moveBad sets a bad ticket aside, never over another one already there.
func (q Queue) moveBad(path, why string) error {
	bad := filepath.Join(q.dir(), badName)
	to := filepath.Join(bad, filepath.Base(path))
	for n := 1; ; n++ {
		if _, err := os.Lstat(to); errors.Is(err, os.ErrNotExist) {
			break
		}
		to = filepath.Join(bad, fmt.Sprintf("%s.%d", filepath.Base(path), n))
	}
	err := os.Rename(path, to)
	if errors.Is(err, os.ErrNotExist) {
		// Gone since the folder was read, taken by a person or a process that does not hold the lock: nothing left to set aside.
		return nil
	}
	if err != nil {
		return unusable("cannot move %s, which %s, to %s: %v", path, why, bad, err)
	}
	fmt.Fprintf(q.log(), "queue: %s %s; moved to %s and skipped\n", path, why, to)
	return nil
}

// writeWhole writes data to path through a temporary file beside it (G-CODE-5); its name starts with a dot, so no sweep takes it for a ticket.
func writeWhole(dir, path string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
