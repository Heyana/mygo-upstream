package platform

import "testing"

// TestCursorCountGrowsWithTheLastShape keeps CursorCount in step with the
// const block: a shape appended after cursorCount, or one inserted before
// it, leaves the count wrong and every backend's table short.
func TestCursorCountGrowsWithTheLastShape(t *testing.T) {
	if int(CursorZoomOut) != CursorCount-1 {
		t.Errorf("CursorZoomOut is %d, CursorCount is %d: the last shape and the count disagree", CursorZoomOut, CursorCount)
	}
	if CursorCount != 31 {
		t.Errorf("%d pointer shapes; CSS names 36, and 31 is what ui/keys.go lists", CursorCount)
	}
}
