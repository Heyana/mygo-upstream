package platform

import (
	"sync/atomic"
	"time"
)

// NoteInput records that the person did something at this moment. Every
// backend calls it from the events that are theirs — the pointer, the keys,
// the wheel — and from nothing else: a frame the content asked for and a
// resize the window reports are the framework's own, and an app waiting for a
// moment nobody is using would never find one on a window that draws as it is
// resized.
func NoteInput() { noteInput() }

// LastInput is NoteInput's reading, in the form the framework's LastInput
// hands to a view.
func LastInput() time.Time {
	if ns := inputAt.Load(); ns != 0 {
		return time.Unix(0, ns)
	}
	return time.Time{}
}

// inputAt is the last input event's time in nanoseconds, in the form every
// backend can write it in from whichever thread delivers the event.
var inputAt atomic.Int64

func noteInput() { inputAt.Store(time.Now().UnixNano()) }
