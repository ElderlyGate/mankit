package queue

import "time"

// Times shortens the queue's fixed staleAfter and touchEvery for one test, and gives back the func that restores them.
func Times(stale, touch time.Duration) (restore func()) {
	oldStale, oldTouch := staleAfter, touchEvery
	staleAfter, touchEvery = stale, touch
	return func() { staleAfter, touchEvery = oldStale, oldTouch }
}

// LeaveFor shortens how long leaving waits for the folder's lock, for one test, and gives back the func that restores it.
func LeaveFor(d time.Duration) (restore func()) {
	old := leaveFor
	leaveFor = d
	return func() { leaveFor = old }
}

// LastPause is the longest pause of a ticket further back in the line, the wait a quick hand-over never takes.
func LastPause() time.Duration { return lastPause }
