package queue_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ElderlyGate/contracts"
	"github.com/gofrs/flock"

	"github.com/tugbayatilla/mankit/queue"
	"github.com/tugbayatilla/mankit/serve"
	"github.com/tugbayatilla/mankit/settings"
)

// The environment that starts this test binary again as another process on the same queue: what it does, where, and with what.
const (
	childEnv  = "MANKIT_QUEUE_CHILD"
	folderEnv = "MANKIT_QUEUE_FOLDER"
	argEnv    = "MANKIT_QUEUE_ARG"
	timesEnv  = "MANKIT_QUEUE_TIMES"
)

// TestMain lets a test start this binary again as another process that changes the same thing.
func TestMain(m *testing.M) {
	if mode := os.Getenv(childEnv); mode != "" {
		os.Exit(child(mode, os.Getenv(folderEnv), os.Getenv(argEnv)))
	}
	os.Exit(m.Run())
}

// child is another process on the queue: count adds one to a counter five times, hold keeps the turn for a while, mark writes its mark once its turn comes, lock holds the folder's lock for a while.
func child(mode, folder, arg string) int {
	if times := os.Getenv(timesEnv); times != "" {
		stale, touch, _ := strings.Cut(times, ",")
		s, _ := time.ParseDuration(stale)
		t, _ := time.ParseDuration(touch)
		queue.Times(s, t)
	}
	q := queue.Queue{Folder: folder, App: "shop", Wait: time.Minute, Log: io.Discard}
	counter, log := filepath.Join(folder, "counter"), filepath.Join(folder, "log")
	var err error
	switch mode {
	case "count":
		for range 5 {
			if err = q.Change(context.Background(), "count one", func(context.Context) error {
				appendLine(log, fmt.Sprintf("start %d", os.Getpid()))
				n := readCount(counter)
				time.Sleep(5 * time.Millisecond)
				if err := os.WriteFile(counter, []byte(strconv.Itoa(n+1)), 0o600); err != nil {
					return err
				}
				appendLine(log, fmt.Sprintf("end %d", os.Getpid()))
				return nil
			}, nothing); err != nil {
				break
			}
		}
	case "hold":
		d, _ := time.ParseDuration(arg)
		err = q.Change(context.Background(), "hold the turn", func(context.Context) error { time.Sleep(d); return nil }, nothing)
	case "mark":
		err = q.Change(context.Background(), "mark "+arg, func(context.Context) error { appendLine(log, arg); return nil }, nothing)
	case "lock":
		// Holds the folder's lock as another process in the middle of reading the line would.
		d, _ := time.ParseDuration(arg)
		f := flock.New(filepath.Join(folder, ".queue", "lock"))
		var got bool
		if got, err = f.TryLock(); err == nil && got {
			appendLine(filepath.Join(folder, "locked"), "")
			time.Sleep(d)
			err = f.Close()
		} else if err == nil {
			err = errors.New("the folder's lock is held")
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// start runs this test binary as another process doing mode; times shortens its fixed waits as "stale,touch", empty keeps them.
func start(t *testing.T, mode, folder, arg, times string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), childEnv+"="+mode, folderEnv+"="+folder, argEnv+"="+arg, timesEnv+"="+times)
	out := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("%s process said: %s", mode, out.String())
		}
	})
	return cmd
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

func readCount(path string) int {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	n, err := strconv.Atoi(string(data))
	if err != nil {
		panic(err)
	}
	return n
}

// syncBuffer is a log many goroutines write to.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newQueue(t *testing.T) (queue.Queue, *syncBuffer) {
	t.Helper()
	log := &syncBuffer{}
	return queue.Queue{Folder: filepath.Join(t.TempDir(), ".shop"), App: "shop", Version: "1.0.0", Wait: 5 * time.Second, Log: log}, log
}

func nothing(context.Context) error { return nil }

// hold runs a change that keeps the turn until the returned func is called, and gives back what Change said.
func hold(t *testing.T, q queue.Queue) (release func() error) {
	t.Helper()
	running, stop, result := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		result <- q.Change(context.Background(), "hold the turn", func(context.Context) error {
			close(running)
			<-stop
			return nil
		}, nothing)
	}()
	select {
	case <-running:
	case err := <-result:
		t.Fatalf("the holding change did not run: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the holding change never got its turn")
	}
	return func() error {
		close(stop)
		return <-result
	}
}

// waitFor polls the queue until it holds n tickets.
func waitFor(t *testing.T, q queue.Queue, n int) queue.Listing {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		l, err := q.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(l.Tickets) == n {
			return l
		}
		if time.Now().After(deadline) {
			t.Fatalf("the queue holds %d tickets, want %d", len(l.Tickets), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func code(err error) string {
	var e *serve.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// plant writes a ticket by hand, as another process would have left it, last touched at touched.
func plant(t *testing.T, q queue.Queue, name, body string, touched time.Time) string {
	t.Helper()
	dir := filepath.Join(q.Folder, ".queue")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, touched, touched); err != nil {
		t.Fatal(err)
	}
	return path
}

const firstID = "00000000000000000001"

func ticketBody(id string) string {
	return `{"apiVersion":"elderlygate/mankit/queue-ticket/v1","kind":"QueueTicket","metadata":{"id":"` + id + `","app":"shop","pid":4242,"host":"build-01"},` +
		`"spec":{"change":"left behind","joinedAt":"2026-11-11T12:00:00Z"}}`
}

// Success

func TestAChangeIsWrittenThenReadBackAndLeavesAnEmptyQueue(t *testing.T) {
	q, _ := newQueue(t)
	var steps []string
	err := q.Change(context.Background(), "add item", func(context.Context) error {
		steps = append(steps, "write")
		return nil
	}, func(context.Context) error {
		steps = append(steps, "read back")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(steps, ",") != "write,read back" {
		t.Fatalf("steps %v, want write then read back", steps)
	}
	l, err := q.List(context.Background())
	if err != nil || len(l.Tickets) != 0 || len(l.Bad) != 0 {
		t.Fatalf("got %+v, %v; want an empty queue", l, err)
	}
}

func TestChangesRunOneAtATimeInTheOrderTheyJoined(t *testing.T) {
	q, _ := newQueue(t)
	release := hold(t, q)
	var mu sync.Mutex
	var ran []int
	running := 0
	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := q.Change(context.Background(), fmt.Sprintf("change %d", i), func(context.Context) error {
				mu.Lock()
				running++
				if running > 1 {
					t.Error("two changes ran at once")
				}
				mu.Unlock()
				time.Sleep(10 * time.Millisecond)
				mu.Lock()
				running--
				ran = append(ran, i)
				mu.Unlock()
				return nil
			}, nothing)
			if err != nil {
				t.Error(err)
			}
		}()
		// One at a time, so the order they joined in is known.
		waitFor(t, q, i+2)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if fmt.Sprint(ran) != "[0 1 2 3 4]" {
		t.Fatalf("ran in the order %v, want the order they joined", ran)
	}
}

func TestTheListShowsTheLineInOrderAndWhoseTurnItIs(t *testing.T) {
	q, _ := newQueue(t)
	release := hold(t, q)
	done := make(chan error, 1)
	go func() { done <- q.Change(context.Background(), "tidy", nothing, nothing) }()
	l := waitFor(t, q, 2)
	if !l.Tickets[0].Turn || l.Tickets[1].Turn {
		t.Errorf("turns %v %v, want the first only", l.Tickets[0].Turn, l.Tickets[1].Turn)
	}
	if l.Tickets[0].Ticket.Spec.Change != "hold the turn" || l.Tickets[1].Ticket.Spec.Change != "tidy" {
		t.Errorf("got %q then %q", l.Tickets[0].Ticket.Spec.Change, l.Tickets[1].Ticket.Spec.Change)
	}
	if m := l.Tickets[1].Ticket.Metadata; m.App != "shop" || m.PID != os.Getpid() || m.Host == "" {
		t.Errorf("metadata %+v does not name this app and process", m)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTheQueueFolderIsThisUsersAloneAndKeptOutOfGit(t *testing.T) {
	q, _ := newQueue(t)
	if err := os.MkdirAll(filepath.Join(q.Folder, ".queue"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := q.Change(context.Background(), "add item", nothing, nothing); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".queue", filepath.Join(".queue", "bad")} {
		info, err := os.Stat(filepath.Join(q.Folder, dir))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s has mode %04o, want 0700", dir, info.Mode().Perm())
		}
	}
	ignore, err := os.ReadFile(filepath.Join(q.Folder, ".queue", ".gitignore"))
	if err != nil || string(ignore) != "*\n" {
		t.Fatalf("got %q, %v; want a .gitignore that ignores everything", ignore, err)
	}
}

// Contract

func TestTicketsOnDiskAndTheListMeetTheContract(t *testing.T) {
	q, _ := newQueue(t)
	plant(t, q, "not-a-ticket.txt", "hello", time.Now())
	release := hold(t, q)
	done := make(chan error, 1)
	go func() { done <- q.Change(context.Background(), "tidy", nothing, nothing) }()
	l := waitFor(t, q, 2)

	files, err := filepath.Glob(filepath.Join(q.Folder, ".queue", "*.json"))
	if err != nil || len(files) != 2 {
		t.Fatalf("tickets on disk %v, %v; want 2", files, err)
	}
	for _, f := range files {
		doc, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := contracts.Validate("mankit/v1/queue-ticket.schema.json", doc); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if len(l.Bad) != 1 {
		t.Fatalf("bad %v, want the one file that is not a ticket", l.Bad)
	}
	app := serve.App{ID: "shop", Version: "1.0.0", Contracts: []string{"v1"}}
	var answer bytes.Buffer
	if err := serve.Encode(&answer, app.Answer("", "v1", queue.ListKind, l)); err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("mankit/v1/queue-list.schema.json", answer.Bytes()); err != nil {
		t.Errorf("the list: %v\n%s", err, answer.String())
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAnEmptyQueueListsEmptyArrays(t *testing.T) {
	q, _ := newQueue(t)
	app := serve.App{ID: "shop", Version: "1.0.0", Contracts: []string{"v1"}}
	l, err := q.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var answer bytes.Buffer
	if err := serve.Encode(&answer, app.Answer("", "v1", queue.ListKind, l)); err != nil {
		t.Fatal(err)
	}
	if err := contracts.Validate("mankit/v1/queue-list.schema.json", answer.Bytes()); err != nil {
		t.Errorf("%v\n%s", err, answer.String())
	}
}

// Errors and codes

func TestAChangeThatWaitsTooLongTimesOutAndLeavesTheLine(t *testing.T) {
	q, _ := newQueue(t)
	release := hold(t, q)
	late := q
	late.Wait = 100 * time.Millisecond
	ran := false
	err := late.Change(context.Background(), "late", func(context.Context) error { ran = true; return nil }, nothing)
	if code(err) != queue.CodeTimeout || ran {
		t.Fatalf("got %v (ran %v), want %s and nothing run", err, ran, queue.CodeTimeout)
	}
	if l := waitFor(t, q, 1); l.Tickets[0].Ticket.Spec.Change != "hold the turn" {
		t.Errorf("the line holds %+v, want only the change that holds the turn", l.Tickets)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestATicketWhoseProcessEndedIsRemovedAndSaid(t *testing.T) {
	q, log := newQueue(t)
	path := plant(t, q, firstID+".json", ticketBody(firstID), time.Now())
	if err := q.Change(context.Background(), "add item", nothing, nothing); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the ticket of an ended process is still there: %v", err)
	}
	l, err := q.List(context.Background())
	if err != nil || len(l.Bad) != 0 {
		t.Fatalf("bad %v, %v; want it removed, not set aside", l.Bad, err)
	}
	if !strings.Contains(log.String(), path) || !strings.Contains(log.String(), "ended") {
		t.Errorf("said %q, want the file and that its process ended", log.String())
	}
}

func TestACancelledContextChangesNothing(t *testing.T) {
	q, _ := newQueue(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	if err := q.Change(ctx, "add item", func(context.Context) error { ran = true; return nil }, nothing); !errors.Is(err, context.Canceled) || ran {
		t.Fatalf("got %v (ran %v), want the cancel and nothing run", err, ran)
	}
	if _, err := os.Stat(filepath.Join(q.Folder, ".queue")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the queue folder was made: %v", err)
	}
}

func TestARunningChangeWhoseTicketWasSetAsideStillHoldsTheTurn(t *testing.T) {
	q, _ := newQueue(t)
	release := hold(t, q)
	l := waitFor(t, q, 1)
	id := l.Tickets[0].Ticket.Metadata.ID + ".json"
	if err := os.Rename(filepath.Join(q.Folder, ".queue", id), filepath.Join(q.Folder, ".queue", "bad", id)); err != nil {
		t.Fatal(err)
	}
	next := q
	next.Wait = 300 * time.Millisecond
	ran := false
	if err := next.Change(context.Background(), "next", func(context.Context) error { ran = true; return nil }, nothing); code(err) != queue.CodeTimeout || ran {
		t.Fatalf("got %v (ran %v), want %s: the next change ran beside the running one", err, ran, queue.CodeTimeout)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestAnUnreadableTicketIsMovedToBadSaidAndSkipped(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":       "{",
		"another kind":   strings.Replace(ticketBody(firstID), `"QueueTicket"`, `"Command"`, 1),
		"another id":     ticketBody("00000000000000000002"),
		"another header": strings.Replace(ticketBody(firstID), "elderlygate/mankit/queue-ticket/v1", "elderlygate/mankit/queue-ticket/v2", 1),
		"no change":      strings.Replace(ticketBody(firstID), `"change":"left behind",`, "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			q, log := newQueue(t)
			path := plant(t, q, firstID+".json", body, time.Now())
			if err := q.Change(context.Background(), "add item", nothing, nothing); err != nil {
				t.Fatal(err)
			}
			l, err := q.List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(l.Bad) != 1 || l.Bad[0].File != firstID+".json" {
				t.Fatalf("bad %v, want the unreadable ticket", l.Bad)
			}
			if !strings.Contains(log.String(), path) {
				t.Errorf("said %q, want the file named", log.String())
			}
		})
	}
}

func TestATicketWithoutATwentyDigitIDIsMovedToBad(t *testing.T) {
	q, _ := newQueue(t)
	plant(t, q, "42.json", ticketBody("42"), time.Now())
	if err := q.Change(context.Background(), "add item", nothing, nothing); err != nil {
		t.Fatal(err)
	}
	l, err := q.List(context.Background())
	if err != nil || len(l.Bad) != 1 || l.Bad[0].File != "42.json" {
		t.Fatalf("bad %v, %v; want the short id set aside", l.Bad, err)
	}
}

func TestABadTicketNeverReplacesOneAlreadyInBad(t *testing.T) {
	q, _ := newQueue(t)
	plant(t, q, firstID+".json", "{", time.Now())
	if err := q.Change(context.Background(), "one", nothing, nothing); err != nil {
		t.Fatal(err)
	}
	plant(t, q, firstID+".json", "[", time.Now())
	if err := q.Change(context.Background(), "two", nothing, nothing); err != nil {
		t.Fatal(err)
	}
	l, err := q.List(context.Background())
	if err != nil || len(l.Bad) != 2 {
		t.Fatalf("bad %v, %v; want both kept", l.Bad, err)
	}
}

func TestAChangeThatCannotBeReadBackIsNotDoneAndTheNextOneRuns(t *testing.T) {
	q, _ := newQueue(t)
	err := q.Change(context.Background(), "add item", nothing, func(context.Context) error { return errors.New("the file is empty") })
	if code(err) != queue.CodeNotReadBack || !strings.Contains(err.Error(), "the file is empty") {
		t.Fatalf("got %v, want %s with the reason", err, queue.CodeNotReadBack)
	}
	if err := q.Change(context.Background(), "next", nothing, nothing); err != nil {
		t.Fatalf("the next change did not run: %v", err)
	}
}

func TestAChangeThatFailsGivesItsOwnErrorAndTheNextOneRuns(t *testing.T) {
	q, _ := newQueue(t)
	own := serve.Fail(serve.CodeNotFound, "no such item")
	readBack := false
	err := q.Change(context.Background(), "remove item", func(context.Context) error { return own }, func(context.Context) error { readBack = true; return nil })
	if !errors.Is(err, own) || readBack {
		t.Fatalf("got %v (read back %v), want the change's own error and no read-back", err, readBack)
	}
	if err := q.Change(context.Background(), "next", nothing, nothing); err != nil {
		t.Fatalf("the next change did not run: %v", err)
	}
}

func TestAWaitingTicketTakenForStaleIsLost(t *testing.T) {
	q, _ := newQueue(t)
	release := hold(t, q)
	done := make(chan error, 1)
	ran := false
	go func() {
		done <- q.Change(context.Background(), "tidy", func(context.Context) error { ran = true; return nil }, nothing)
	}()
	l := waitFor(t, q, 2)
	waiting := l.Tickets[1].Ticket.Metadata.ID + ".json"
	// As another process does when it finds the ticket stale.
	if err := os.Rename(filepath.Join(q.Folder, ".queue", waiting), filepath.Join(q.Folder, ".queue", "bad", waiting)); err != nil {
		t.Fatal(err)
	}
	if err := <-done; code(err) != queue.CodeLost || ran {
		t.Fatalf("got %v (ran %v), want %s and nothing run", err, ran, queue.CodeLost)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestAQueueFolderThatCannotBeMadeIsSaidWithItsCode(t *testing.T) {
	q, _ := newQueue(t)
	if err := os.WriteFile(q.Folder, []byte("a file where the app folder should be"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := q.Change(context.Background(), "add item", nothing, nothing); code(err) != queue.CodeUnusable {
		t.Fatalf("got %v, want %s", err, queue.CodeUnusable)
	}
}

func TestACancelledWaitLeavesTheLine(t *testing.T) {
	q, _ := newQueue(t)
	release := hold(t, q)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.Change(ctx, "tidy", nothing, nothing) }()
	waitFor(t, q, 2)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the cancel", err)
	}
	waitFor(t, q, 1)
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

// The run limit

func TestAChangePastTheRunLimitIsToldToStopAndFails(t *testing.T) {
	q, _ := newQueue(t)
	q.Limit = 100 * time.Millisecond
	readBack := false
	err := q.Change(context.Background(), "slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}, func(context.Context) error { readBack = true; return nil })
	if code(err) != queue.CodeTooLong || readBack {
		t.Fatalf("got %v (read back %v), want %s and no read-back", err, readBack, queue.CodeTooLong)
	}
	if err := q.Change(context.Background(), "next", nothing, nothing); err != nil {
		t.Fatalf("the next change did not run: %v", err)
	}
}

func TestAWriteThatFinishesPastTheLimitIsReadBackAndKeepsTheTurnUntilThen(t *testing.T) {
	q, _ := newQueue(t)
	q.Limit = 100 * time.Millisecond
	writing, readEnd := make(chan struct{}), make(chan time.Time, 1)
	result := make(chan error, 1)
	readBack := false
	go func() {
		result <- q.Change(context.Background(), "deaf", func(context.Context) error {
			close(writing)
			time.Sleep(300 * time.Millisecond)
			return nil
		}, func(ctx context.Context) error {
			time.Sleep(200 * time.Millisecond)
			readBack = ctx.Err() == nil
			readEnd <- time.Now()
			return nil
		})
	}()
	<-writing
	var ranAt time.Time
	if err := q.Change(context.Background(), "next", func(context.Context) error { ranAt = time.Now(); return nil }, nothing); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil || !readBack {
		t.Fatalf("got %v (read back with a live context %v); a write that finished is done once read back", err, readBack)
	}
	if ended := <-readEnd; ranAt.Before(ended) {
		t.Fatal("the next change ran before the slow read-back ended")
	}
}

func TestAWriteThatFinishedPastTheLimitButCannotBeReadBackIsNotReadBack(t *testing.T) {
	q, _ := newQueue(t)
	q.Limit = 50 * time.Millisecond
	err := q.Change(context.Background(), "slow", func(context.Context) error {
		time.Sleep(150 * time.Millisecond)
		return nil
	}, func(context.Context) error { return errors.New("the file is half written") })
	if code(err) != queue.CodeNotReadBack {
		t.Fatalf("got %v, want %s, never %s for a write that finished", err, queue.CodeNotReadBack, queue.CodeTooLong)
	}
}

func TestChangesHandTheTurnOnQuickly(t *testing.T) {
	q, _ := newQueue(t)
	began := time.Now()
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 2 {
				if err := q.Change(context.Background(), "quick", func(context.Context) error { time.Sleep(time.Millisecond); return nil }, nothing); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if took := time.Since(began); took > time.Second {
		t.Fatalf("20 changes of 1ms took %s, want well under a second", took)
	}
}

func TestATicketLeavesTheLineEvenWhenTheFolderLockIsHeldElsewhere(t *testing.T) {
	defer queue.LeaveFor(100 * time.Millisecond)()
	q, log := newQueue(t)
	writing, finish, result := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		result <- q.Change(context.Background(), "held up", func(context.Context) error {
			close(writing)
			<-finish
			return nil
		}, nothing)
	}()
	<-writing
	l := waitFor(t, q, 1)
	ticket := filepath.Join(q.Folder, ".queue", l.Tickets[0].Ticket.Metadata.ID+".json")
	start(t, "lock", q.Folder, "700ms", "")
	waitForFile(t, filepath.Join(q.Folder, "locked"))
	close(finish)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{ticket, strings.TrimSuffix(ticket, ".json") + ".lock"} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still there: %v", path, err)
		}
	}
	if !strings.Contains(log.String(), ticket) {
		t.Errorf("said %q, want the ticket named", log.String())
	}
	if err := q.Change(context.Background(), "next", nothing, nothing); err != nil {
		t.Fatalf("the next change in this process did not run: %v", err)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never came", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAWaitThatTimesOutNamesTheHolderAndTheListSaysSinceWhen(t *testing.T) {
	q, _ := newQueue(t)
	before := time.Now()
	release := hold(t, q)
	l := waitFor(t, q, 1)
	h := l.Tickets[0]
	if !h.Turn || h.Since == nil || h.Since.Before(before.Add(-time.Second)) || h.Since.After(time.Now()) {
		t.Fatalf("holder %+v, want its turn and since when", h)
	}
	late := q
	late.Wait = 150 * time.Millisecond
	err := late.Change(context.Background(), "late", nothing, nothing)
	if code(err) != queue.CodeTimeout {
		t.Fatalf("got %v, want %s", err, queue.CodeTimeout)
	}
	m := h.Ticket.Metadata
	for _, want := range []string{m.ID, fmt.Sprintf("pid %d", m.PID), m.Host, "held it for"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q does not name %q", err.Error(), want)
		}
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

// Settings

func TestTheWaitAndLimitComeFromTheAppsSettingsRows(t *testing.T) {
	q, _ := newQueue(t)
	set, err := q.Settings("45s", "5m")
	if err != nil || set.Wait != 45*time.Second || set.Limit != 5*time.Minute {
		t.Fatalf("got wait %s limit %s, %v; want 45s and 5m", set.Wait, set.Limit, err)
	}
	if kept, err := q.Settings("", ""); err != nil || kept.Wait != q.Wait || kept.Limit != q.Limit {
		t.Fatalf("empty rows changed the waits: %+v, %v", kept, err)
	}
	for _, k := range []settings.Key{queue.WaitKey, queue.LimitKey} {
		if k.Help == "" || !strings.HasPrefix(k.Name, "queue.") {
			t.Errorf("%+v needs a queue. name and a purpose line", k)
		}
	}
	if queue.WaitKey.Default != queue.DefaultWait.String() || queue.LimitKey.Default != queue.DefaultLimit.String() {
		t.Errorf("the rows' defaults %s, %s are not the queue's", queue.WaitKey.Default, queue.LimitKey.Default)
	}
}

func TestAWaitOrLimitThatIsNoDurationOrTooShortIsRefusedNamingTheRow(t *testing.T) {
	q, _ := newQueue(t)
	for _, c := range []struct{ wait, limit, row string }{
		{"soon", "", "queue.wait"},
		{"", "-1m", "queue.limit"},
		{"1ms", "", "queue.wait"},
		{"", "0s", "queue.limit"},
	} {
		_, err := q.Settings(c.wait, c.limit)
		if code(err) != serve.CodeConfig || !strings.Contains(err.Error(), c.row) {
			t.Errorf("%q %q: got %v, want %s naming %s", c.wait, c.limit, err, serve.CodeConfig, c.row)
		}
	}
}

func TestALockFileLeftWithoutItsTicketIsRemovedNeverSetAside(t *testing.T) {
	q, log := newQueue(t)
	path := plant(t, q, firstID+".lock", "", time.Now())
	if err := q.Change(context.Background(), "add item", nothing, nothing); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the orphan lock file is still there: %v", err)
	}
	l, err := q.List(context.Background())
	if err != nil || len(l.Bad) != 0 || log.String() != "" {
		t.Fatalf("bad %v, said %q, %v; want it removed quietly", l.Bad, log.String(), err)
	}
	locks, _ := filepath.Glob(filepath.Join(q.Folder, ".queue", "*.lock"))
	for _, l := range locks {
		if filepath.Base(l) != "turn.lock" {
			t.Errorf("%s was left behind by a change that is done", l)
		}
	}
}

// The backend marker

func marker(t *testing.T, q queue.Queue, body string) string {
	t.Helper()
	return plant(t, q, "backend", body, time.Now())
}

func markerOf(kind string) string {
	return `{"apiVersion":"elderlygate/mankit/queue-backend/v1","kind":"QueueBackend","spec":{"type":"` + kind + `"}}`
}

func TestAMarkerNamingFilesKeepsTheFileQueueAndStays(t *testing.T) {
	q, log := newQueue(t)
	path := marker(t, q, markerOf(queue.BackendFiles))
	if err := contracts.Validate("mankit/v1/queue-backend.schema.json", []byte(markerOf(queue.BackendFiles))); err != nil {
		t.Fatalf("the marker the test writes breaks the contract: %v", err)
	}
	if err := q.Change(context.Background(), "add item", nothing, nothing); err != nil {
		t.Fatal(err)
	}
	l, err := q.List(context.Background())
	if err != nil || len(l.Bad) != 0 {
		t.Fatalf("bad %v, %v; want the marker left alone", l.Bad, err)
	}
	if _, err := os.Stat(path); err != nil || log.String() != "" {
		t.Fatalf("the marker went (%v) or was said: %q", err, log.String())
	}
}

func TestABackendThisBuildDoesNotRunIsRefusedNeverTakenForFiles(t *testing.T) {
	for name, body := range map[string]string{
		"lock service":   markerOf(queue.BackendLockService),
		"unknown type":   markerOf("carrier-pigeon"),
		"not JSON":       "{",
		"another header": strings.Replace(markerOf(queue.BackendFiles), "QueueBackend", "Backend", 1),
	} {
		t.Run(name, func(t *testing.T) {
			q, _ := newQueue(t)
			marker(t, q, body)
			ran := false
			err := q.Change(context.Background(), "add item", func(context.Context) error { ran = true; return nil }, nothing)
			if code(err) != queue.CodeBackendUnsupported || ran {
				t.Fatalf("got %v (ran %v), want %s and nothing run", err, ran, queue.CodeBackendUnsupported)
			}
			if _, err := q.List(context.Background()); code(err) != queue.CodeBackendUnsupported {
				t.Fatalf("list got %v, want %s", err, queue.CodeBackendUnsupported)
			}
			if tickets, _ := filepath.Glob(filepath.Join(q.Folder, ".queue", "*.json")); len(tickets) != 0 {
				t.Fatalf("tickets %v were written", tickets)
			}
		})
	}
}

// Across processes

func TestProcessesChangingOneThingTakeTurns(t *testing.T) {
	folder := filepath.Join(t.TempDir(), ".shop")
	var children []*exec.Cmd
	for range 3 {
		children = append(children, start(t, "count", folder, "", ""))
	}
	for _, cmd := range children {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("a process failed: %v", err)
		}
	}
	if n := readCount(filepath.Join(folder, "counter")); n != 15 {
		t.Fatalf("counted %d, want 15: a change was lost to another running at once", n)
	}
	log, err := os.ReadFile(filepath.Join(folder, "log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(strings.ReplaceAll(string(log), "\n", " "))
	for i := 0; i+3 < len(lines); i += 4 {
		if lines[i] != "start" || lines[i+2] != "end" || lines[i+1] != lines[i+3] {
			t.Fatalf("changes overlapped at %v", lines[i:i+4])
		}
	}
}

func TestProcessesGoInTheOrderTheyJoined(t *testing.T) {
	q, _ := newQueue(t)
	release := hold(t, q)
	var children []*exec.Cmd
	for i := range 3 {
		children = append(children, start(t, "mark", q.Folder, strconv.Itoa(i), ""))
		waitFor(t, q, i+2)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range children {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("a process failed: %v", err)
		}
	}
	log, err := os.ReadFile(filepath.Join(q.Folder, "log"))
	if err != nil || strings.Join(strings.Fields(string(log)), " ") != "0 1 2" {
		t.Fatalf("marks %q, %v; want 0 1 2, the order they joined", log, err)
	}
}

// turnOf waits until a ticket of another process holds the turn, and gives back its id.
func turnOf(t *testing.T, q queue.Queue) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		l, err := q.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(l.Tickets) > 0 && l.Tickets[0].Turn && l.Tickets[0].Ticket.Metadata.PID != os.Getpid() {
			return l.Tickets[0].Ticket.Metadata.ID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no other process got the turn")
	return ""
}

func TestAKilledProcessThatHeldTheTurnIsRecoveredAtOnce(t *testing.T) {
	q, log := newQueue(t)
	holder := start(t, "hold", q.Folder, "1h", "")
	id := turnOf(t, q)
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait()
	began := time.Now()
	if err := q.Change(context.Background(), "next", nothing, nothing); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(began); waited > 2*time.Second {
		t.Errorf("waited %s after the holder died", waited)
	}
	if !strings.Contains(log.String(), id) || !strings.Contains(log.String(), "ended") {
		t.Errorf("said %q, want the dead ticket named", log.String())
	}
}

func TestARunningProcessWhoseTicketWasSetAsideStillHoldsTheTurn(t *testing.T) {
	q, _ := newQueue(t)
	start(t, "hold", q.Folder, "1h", "")
	id := turnOf(t, q) + ".json"
	if err := os.Rename(filepath.Join(q.Folder, ".queue", id), filepath.Join(q.Folder, ".queue", "bad", id)); err != nil {
		t.Fatal(err)
	}
	q.Wait = 500 * time.Millisecond
	ran := false
	if err := q.Change(context.Background(), "next", func(context.Context) error { ran = true; return nil }, nothing); code(err) != queue.CodeTimeout || ran {
		t.Fatalf("got %v (ran %v), want %s: the next change ran beside the running one", err, ran, queue.CodeTimeout)
	}
}

func TestALongChangeKeepsItsTicketThroughManyStaleTimes(t *testing.T) {
	defer queue.Times(200*time.Millisecond, 50*time.Millisecond)()
	q, log := newQueue(t)
	holder := start(t, "hold", q.Folder, "1s", "200ms,50ms")
	turnOf(t, q)
	for range 20 {
		if _, err := q.List(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := holder.Wait(); err != nil {
		t.Fatalf("the long change failed: %v", err)
	}
	if l, err := q.List(context.Background()); err != nil || len(l.Bad) != 0 || log.String() != "" {
		t.Fatalf("bad %v, said %q, %v; want the live ticket left alone", l.Bad, log.String(), err)
	}
}

func TestATicketWhoseOwnerShowsNoSignOfLifeIsSetAsideAndSaid(t *testing.T) {
	defer queue.Times(300*time.Millisecond, time.Hour)()
	q, log := newQueue(t)
	start(t, "hold", q.Folder, "1h", "30s,1h")
	id := turnOf(t, q)
	time.Sleep(400 * time.Millisecond)
	l, err := q.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Tickets) != 0 || len(l.Bad) != 1 || l.Bad[0].File != id+".json" {
		t.Fatalf("got %+v, want the silent ticket in bad/", l)
	}
	if !strings.Contains(log.String(), "stale") {
		t.Errorf("said %q, want it named stale", log.String())
	}
}
