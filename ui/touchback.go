package ui

// The back gesture — a touch from the left edge previewing the previous
// entry — is not ported yet. Neither half exists here: no touch ever starts
// a preview, so nothing ever needs cancelling.
//
// cancelTouchBack is where the gesture layer reports that a pinch has taken
// the contacts (gesture.go), and it stays a no-op until the preview lands.
// It exists now because the alternative is worse: without it, whoever ports
// the back gesture has to remember there is a second caller to keep in step,
// and the pinch path would drift from the reference.
func (rt *engine) cancelTouchBack() {}
