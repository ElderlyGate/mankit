package queue

import "time"

// Times shortens the queue's fixed staleAfter and touchEvery for one test, and gives back the func that restores them.
func Times(stale, touch time.Duration) (restore func()) {
	oldStale, oldTouch := staleAfter, touchEvery
	staleAfter, touchEvery = stale, touch
	return func() { staleAfter, touchEvery = oldStale, oldTouch }
}
