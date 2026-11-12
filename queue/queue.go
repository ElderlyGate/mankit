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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"github.com/tugbayatilla/mankit/home"
	"github.com/tugbayatilla/mankit/serve"
	"github.com/tugbayatilla/mankit/settings"
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

// The waits an app gets when it sets none; each only bounds this process's own waiting and holding, so apps may set them apart.
const (
	DefaultWait  = 30 * time.Second
	DefaultLimit = 2 * time.Minute
)

// minSetting is the least a user may set queue.wait or queue.limit to, so a typo such as 1ms does not fail every change.
const minSetting = time.Second

// WaitKey and LimitKey are the rows every app adds to its settings, so users set the queue's waits the same way in each.
var (
	WaitKey  = settings.Key{Name: "queue.wait", Default: DefaultWait.String(), Help: "how long a change waits for its turn before it gives up"}
	LimitKey = settings.Key{Name: "queue.limit", Default: DefaultLimit.String(), Help: "how long a change may hold its turn before it is stopped"}
)

// Settings is q with Wait and Limit from the rows queue.wait and queue.limit (empty keeps them), refusing a value under a second with serve's config code.
func (q Queue) Settings(wait, limit string) (Queue, error) {
	for _, row := range []struct {
		key   settings.Key
		value string
		to    *time.Duration
	}{{WaitKey, wait, &q.Wait}, {LimitKey, limit, &q.Limit}} {
		if row.value == "" {
			continue
		}
		d, err := time.ParseDuration(row.value)
		if err != nil || d < minSetting {
			return q, serve.Fail(serve.CodeConfig, fmt.Sprintf("%s is %q; it takes a duration of at least %s, such as %s", row.key.Name, row.value, minSetting, row.key.Default))
		}
		*row.to = d
	}
	return q, nil
}

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
	lockSuffix  = ".lock" // the lock file beside a ticket or turn, since a lock on Windows keeps others from reading what it covers
	ignoreName  = ".gitignore"
	backendName = "backend"
)

// staleAfter and touchEvery are fixed by contracts mankit/v1, so every app sharing a queue agrees; vars only so tests can shorten them.
var (
	staleAfter = 30 * time.Second
	touchEvery = 5 * time.Second
	leaveFor   = 5 * time.Second // how long leaving waits for the folder's lock; a var only so a test can shorten it
)

const (
	firstPause  = 5 * time.Millisecond
	lastPause   = 200 * time.Millisecond
	firstInLine = 20 * time.Millisecond // the longest pause of a ticket that is first, or of a process waiting for the folder's lock, so a turn passes on quickly
	readBackFor = 5 * time.Second
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
	Limit   time.Duration // how long a change may hold the turn, enforced by this process on itself; zero is DefaultLimit
	Log     io.Writer     // where a ticket set aside or a failed leave is said; nil is stderr

	proc *inProcess // this process's locks on the folder, found once per Change or List
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

// Change runs write in its turn, with a context ending at Limit that it must honour, then readBack; done only once read back, else a coded error.
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
	q.proc = q.process()
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
	running, stop := context.WithTimeout(ctx, q.limit())
	defer stop()
	if err := write(running); err != nil {
		if errors.Is(running.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return serve.Fail(CodeTooLong, fmt.Sprintf("%s held the turn in %s past the limit of %s and stopped: %v", change, q.dir(), q.limit(), err))
		}
		return err
	}
	// A write that finished is read back whatever the limit says, so what it wrote is reported as it is.
	reading, done := context.WithTimeout(ctx, readBackFor)
	defer done()
	if err := readBack(reading); err != nil {
		return serve.Fail(CodeNotReadBack, fmt.Sprintf("%s was written but could not be read back: %v", change, err))
	}
	return nil
}

// list is the queue now; tickets of dead processes go and bad ones are set aside first, as every reader of the queue does.
func (files) list(ctx context.Context, q Queue) (Listing, error) {
	q.proc = q.process()
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

func (q Queue) limit() time.Duration {
	if q.Limit > 0 {
		return q.Limit
	}
	return DefaultLimit
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

// retry calls try until it succeeds, fails or ctx ends, pausing longer each time up to longest.
func retry(ctx context.Context, longest time.Duration, try func() (bool, error)) error {
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
		pause = min(pause*2, longest)
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
	if q.proc != nil {
		return q.proc
	}
	key := resolved(q.dir())
	p, _ := processes.LoadOrStore(key, &inProcess{folder: make(chan struct{}, 1), mine: map[string]bool{}})
	return p.(*inProcess)
}

// resolved is dir with its links followed, the same before and after the folder is made, so two paths to one folder share one set of locks.
func resolved(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}
	for at, rest := abs, ""; ; {
		if real, err := filepath.EvalSymlinks(at); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(at)
		if parent == at {
			return abs
		}
		rest = filepath.Join(filepath.Base(at), rest)
		at = parent
	}
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

// ticket is this process's place in the line while its change waits and runs; its lock file stays locked for as long, which says it is alive.
type ticket struct {
	q     Queue
	id    string
	path  string
	alive *flock.Flock
	turn  *flock.Flock
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
		// Read again under the lock, as the marker changes only under it: a backend named since Change began is honoured.
		if _, err := q.backend(); err != nil {
			return err
		}
		tickets, err := q.sweep()
		if err != nil {
			return err
		}
		t.id = nextID(tickets, time.Now())
		t.path = filepath.Join(q.dir(), t.id+".json")
		// Locked before the ticket is written and before the folder's lock is given up, so no sweep ever sees this ticket without its owner.
		alive, err := tryLock(lockOf(t.path))
		if err != nil {
			return unusable("cannot lock %s: %v", lockOf(t.path), err)
		}
		if alive == nil {
			return unusable("cannot lock %s: another process holds it", lockOf(t.path))
		}
		t.alive = alive
		q.process().hold(t.path, true)
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
		return nil
	})
	if err != nil {
		if t.alive != nil {
			t.release()
		}
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

// touch keeps the ticket's time fresh, so a reader can tell a hung process, whose lock is still held, from one that runs.
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

// await returns once the ticket is first in the line and holds the turn, or when ctx ends; it looks again soon once the ticket moves up.
func (t *ticket) await(ctx context.Context) error {
	pause, place := firstPause, -1
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ready, at := false, 0
		err := t.q.locked(ctx, func() error {
			var err error
			ready, at, err = t.try()
			return err
		})
		if err != nil || ready {
			return err
		}
		if place >= 0 && at < place {
			pause = firstPause
		}
		place = at
		if at == 0 {
			pause = min(pause, firstInLine)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pause):
		}
		longest := lastPause
		if at == 0 {
			longest = firstInLine
		}
		pause = min(pause*2, longest)
	}
}

// try takes the turn when the ticket is first, and says the ticket's place; the turn's lock also keeps out a change whose ticket was set aside while it still runs.
func (t *ticket) try() (bool, int, error) {
	tickets, err := t.q.sweep()
	if err != nil {
		return false, 0, err
	}
	at := slices.IndexFunc(tickets, func(o Ticket) bool { return o.Metadata.ID == t.id })
	if at < 0 {
		return false, 0, serve.Fail(CodeLost, fmt.Sprintf("the ticket %s was taken for stale and moved to %s; the change did not run", t.path, filepath.Join(t.q.dir(), badName)))
	}
	if at > 0 {
		return false, at, nil
	}
	p := t.q.process()
	if !p.turn.TryLock() {
		return false, 0, nil
	}
	path := filepath.Join(t.q.dir(), turnName)
	turn, err := tryLock(lockOf(path))
	if err != nil || turn == nil {
		p.turn.Unlock()
		if err != nil {
			return false, 0, unusable("cannot lock %s: %v", lockOf(path), err)
		}
		return false, 0, nil
	}
	t.turn = turn
	if err := writeWhole(t.q.dir(), path, []byte(t.id+"\n"+time.Now().UTC().Format(time.RFC3339Nano)+"\n")); err != nil {
		return false, 0, unusable("cannot write %s: %v", path, err)
	}
	return true, 0, nil
}

// tryLock takes the exclusive lock of the lock file at path without waiting, nil when another holds it; the system gives it back when its process ends.
func tryLock(path string) (*flock.Flock, error) {
	f := flock.New(path, flock.SetPermissions(0o600))
	got, err := f.TryLock()
	if err != nil || !got {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// lockOf is the lock file beside path: <id>.lock for a ticket, turn.lock for the turn.
func lockOf(path string) string { return strings.TrimSuffix(path, ".json") + lockSuffix }

// leave gives the turn up and takes the ticket out of the line, whether the change ran or not; it is not cut short by the caller's context.
func (t *ticket) leave() {
	close(t.stop)
	t.done.Wait()
	if t.turn != nil {
		// Cleared while the turn is still held, so no one reads this ticket's id as the next holder's.
		if err := os.Remove(filepath.Join(t.q.dir(), turnName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(t.q.log(), "queue: cannot clear %s: %v\n", filepath.Join(t.q.dir(), turnName), err)
		}
		_ = t.turn.Close()
		t.q.process().turn.Unlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), leaveFor)
	defer cancel()
	err := t.q.locked(ctx, t.remove)
	if err == nil {
		return
	}
	// Ids are never reused, so taking this ticket out without the folder's lock harms no one; leaving it would block the line until it went stale.
	fmt.Fprintf(t.q.log(), "queue: cannot take the ticket %s out of the line under the folder's lock (%v); taking it out without\n", t.path, err)
	if err := t.remove(); err != nil {
		fmt.Fprintf(t.q.log(), "queue: cannot remove the ticket %s: %v\n", t.path, err)
	}
}

// remove takes the ticket out of the line, then gives up its lock file.
func (t *ticket) remove() error {
	err := os.Remove(t.path)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	t.release()
	return err
}

// release closes the ticket's lock file and removes it, once the ticket itself is gone.
func (t *ticket) release() {
	_ = t.alive.Close()
	_ = os.Remove(lockOf(t.path))
	t.q.process().hold(t.path, false)
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
	var f *flock.Flock
	err := retry(ctx, firstInLine, func() (bool, error) {
		var err error
		f, err = tryLock(path)
		return f != nil, err
	})
	if err != nil {
		if ctx.Err() != nil {
			return err
		}
		return unusable("cannot lock %s: %v", path, err)
	}
	defer f.Close()
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
	p := q.process()
	if p.turn.TryLock() {
		defer p.turn.Unlock()
		free, err := tryLock(lockOf(path))
		if err != nil {
			return holder{}, unusable("cannot lock %s: %v", lockOf(path), err)
		}
		if free != nil {
			_ = free.Close()
			return holder{}, nil
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return holder{}, nil
	}
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
	var locks []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, lockSuffix) && name != lockOf(turnName) {
			locks = append(locks, name)
			continue
		}
		if strings.HasPrefix(name, ".") || name == badName || name == lockName || name == turnName || name == lockOf(turnName) || name == backendName {
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
	q.dropOrphanLocks(locks)
	sort.Slice(tickets, func(i, j int) bool { return tickets[i].Metadata.ID < tickets[j].Metadata.ID })
	return tickets, nil
}

// ownerGone removes the ticket at path when its lock file is free: the system gave the lock back, so the process that held it has ended.
func (q Queue) ownerGone(path string) (bool, error) {
	free, err := tryLock(lockOf(path))
	if err != nil || free == nil {
		return false, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = free.Close()
		return false, unusable("cannot remove %s, whose process has ended: %v", path, err)
	}
	// Closed before it is removed, since Windows removes no file that is open.
	_ = free.Close()
	_ = os.Remove(lockOf(path))
	fmt.Fprintf(q.log(), "queue: %s: its process has ended; removed\n", path)
	return true, nil
}

// dropOrphanLocks removes a ticket's lock file left without its ticket once no process holds it; never moved to bad/, as it says nothing.
func (q Queue) dropOrphanLocks(names []string) {
	for _, name := range names {
		ticket := filepath.Join(q.dir(), strings.TrimSuffix(name, lockSuffix)+".json")
		if _, err := os.Lstat(ticket); err == nil || q.process().holds(ticket) {
			continue
		}
		free, err := tryLock(filepath.Join(q.dir(), name))
		if err != nil || free == nil {
			continue
		}
		_ = free.Close()
		_ = os.Remove(filepath.Join(q.dir(), name))
	}
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
