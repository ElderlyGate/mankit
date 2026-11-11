package queue_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ElderlyGate/contracts"

	"github.com/tugbayatilla/mankit/queue"
	"github.com/tugbayatilla/mankit/serve"
)

const childEnv = "MANKIT_QUEUE_CHILD"

// TestMain lets a test start this binary again as another process that changes the same thing.
func TestMain(m *testing.M) {
	if folder := os.Getenv(childEnv); folder != "" {
		os.Exit(child(folder))
	}
	os.Exit(m.Run())
}

// child adds one to a counter file five times, each read and write apart, and logs when each change starts and ends.
func child(folder string) int {
	q := queue.Queue{Folder: folder, App: "shop", Wait: time.Minute}
	counter, log := filepath.Join(folder, "counter"), filepath.Join(folder, "log")
	for range 5 {
		err := q.Change(context.Background(), "count one", func() error {
			appendLine(log, fmt.Sprintf("start %d", os.Getpid()))
			n := readCount(counter)
			time.Sleep(5 * time.Millisecond)
			if err := os.WriteFile(counter, []byte(strconv.Itoa(n+1)), 0o600); err != nil {
				return err
			}
			appendLine(log, fmt.Sprintf("end %d", os.Getpid()))
			return nil
		}, func() error { return nil })
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return 0
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
	return queue.Queue{Folder: filepath.Join(t.TempDir(), ".shop"), App: "shop", Version: "1.0.0", Wait: 5 * time.Second, Stale: 5 * time.Second, Log: log}, log
}

func nothing() error { return nil }

// hold runs a change that keeps the turn until the returned func is called, and gives back what Change said.
func hold(t *testing.T, q queue.Queue) (release func() error) {
	t.Helper()
	running, stop, result := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		result <- q.Change(context.Background(), "hold the turn", func() error {
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
		l, err := q.List()
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
	return `{"apiVersion":"elderlygate/mankit/ticket/v1","kind":"Ticket","metadata":{"id":"` + id + `","app":"shop","pid":4242,"host":"build-01"},` +
		`"spec":{"change":"left behind","joinedAt":"2026-11-11T12:00:00Z"}}`
}

// Success

func TestAChangeIsWrittenThenReadBackAndLeavesAnEmptyQueue(t *testing.T) {
	q, _ := newQueue(t)
	var steps []string
	err := q.Change(context.Background(), "add item", func() error {
		steps = append(steps, "write")
		return nil
	}, func() error {
		steps = append(steps, "read back")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(steps, ",") != "write,read back" {
		t.Fatalf("steps %v, want write then read back", steps)
	}
	l, err := q.List()
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
			err := q.Change(context.Background(), fmt.Sprintf("change %d", i), func() error {
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
		if err := contracts.Validate("mankit/v1/ticket.schema.json", doc); err != nil {
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
	l, err := q.List()
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
	err := late.Change(context.Background(), "late", func() error { ran = true; return nil }, nothing)
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

func TestAStaleTicketIsMovedToBadSaidAndSkipped(t *testing.T) {
	q, log := newQueue(t)
	q.Stale = time.Second
	path := plant(t, q, firstID+".json", ticketBody(firstID), time.Now().Add(-time.Hour))
	if err := q.Change(context.Background(), "add item", nothing, nothing); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(q.Folder, ".queue", "bad", firstID+".json")); err != nil {
		t.Fatalf("the stale ticket is not in bad/: %v", err)
	}
	if !strings.Contains(log.String(), path) || !strings.Contains(log.String(), "stale") {
		t.Errorf("said %q, want the file and that it is stale", log.String())
	}
}

func TestAnUnreadableTicketIsMovedToBadSaidAndSkipped(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":       "{",
		"another kind":   strings.Replace(ticketBody(firstID), `"Ticket"`, `"Command"`, 1),
		"another id":     ticketBody("00000000000000000002"),
		"another header": strings.Replace(ticketBody(firstID), "elderlygate/mankit/ticket/v1", "elderlygate/mankit/ticket/v2", 1),
	} {
		t.Run(name, func(t *testing.T) {
			q, log := newQueue(t)
			path := plant(t, q, firstID+".json", body, time.Now())
			if err := q.Change(context.Background(), "add item", nothing, nothing); err != nil {
				t.Fatal(err)
			}
			l, err := q.List()
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
	l, err := q.List()
	if err != nil || len(l.Bad) != 2 {
		t.Fatalf("bad %v, %v; want both kept", l.Bad, err)
	}
}

func TestAChangeThatCannotBeReadBackIsNotDoneAndTheNextOneRuns(t *testing.T) {
	q, _ := newQueue(t)
	err := q.Change(context.Background(), "add item", nothing, func() error { return errors.New("the file is empty") })
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
	err := q.Change(context.Background(), "remove item", func() error { return own }, func() error { readBack = true; return nil })
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
	go func() { done <- q.Change(context.Background(), "tidy", func() error { ran = true; return nil }, nothing) }()
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

// Across processes

func TestProcessesChangingOneThingTakeTurns(t *testing.T) {
	folder := filepath.Join(t.TempDir(), ".shop")
	var children []*exec.Cmd
	for range 3 {
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), childEnv+"="+folder)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if t.Failed() {
				t.Log(out.String())
			}
		})
		children = append(children, cmd)
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
