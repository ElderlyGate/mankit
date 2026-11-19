package store

import (
	"bytes"
	"runtime"
	"strconv"
	"sync"
)

// running is every change whose fn runs now, by the goroutine running it, so a write fn makes through a path, such as settings.Set, finds the change that already holds its folder's turn instead of waiting for it for ever.
// The goroutine tells a change nested in fn from one another goroutine makes at the same time, which waits its turn as it should.
var running sync.Map // goroutine id -> []*tx

// holding marks x as running on this goroutine until done is called.
func holding(x *tx) (done func()) {
	id := goroutine()
	was, _ := running.Load(id)
	list, _ := was.([]*tx)
	running.Store(id, append(list, x))
	return func() {
		now, _ := running.Load(id)
		list, _ := now.([]*tx)
		if len(list) <= 1 {
			running.Delete(id)
			return
		}
		running.Store(id, list[:len(list)-1])
	}
}

// heldHere is the change running on this goroutine that holds the turn of the line, nil when none does.
func heldHere(line string) *tx {
	now, ok := running.Load(goroutine())
	if !ok {
		return nil
	}
	list := now.([]*tx)
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].t.lines[line] {
			return list[i]
		}
	}
	return nil
}

// goroutine is the id of the goroutine running it, as the runtime's own stack trace names it ("goroutine 7 [running]:").
func goroutine() uint64 {
	var b [64]byte
	n := runtime.Stack(b[:], false)
	f := bytes.TrimPrefix(b[:n], []byte("goroutine "))
	if i := bytes.IndexByte(f, ' '); i > 0 {
		f = f[:i]
	}
	id, _ := strconv.ParseUint(string(f), 10, 64)
	return id
}
