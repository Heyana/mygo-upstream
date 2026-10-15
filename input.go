package mygo

import (
	"time"

	"github.com/egoist/mygo/internal/platform"
)

// LastInput reports when the app last received an event from the person
// using it — a pointer move, a click, a key, a scroll — and the zero time
// when it has received none since it started.
//
// It is how an app tells a moment someone is using it from one nobody is,
// which is what work that has to be done ahead of time needs: decoding the
// pictures of the next screenful takes as long either way, but decoded in
// the frame that draws them it costs that frame — the whole frame, if it is
// a picture — and decoded in a quiet moment it costs nothing anyone can see.
//
//	// While the person is scrolling, the frame that brings a picture on
//	// screen owns the time it takes to decode it. While they are not,
//	// a pass over the next screenful can have it.
//	if time.Since(mygo.LastInput()) > 200*time.Millisecond {
//		prepare()
//	}
//
// Where the framework delivers an event of its own — a resize, a frame the
// window asked for — that is not input and does not count: an app waiting
// for a quiet moment would otherwise never find one on a window that draws
// as it is resized.
func LastInput() time.Time { return platform.LastInput() }
