package ui

import (
	"strconv"
	"testing"
)

// A list that scrolls itself, inside a container that scrolls, takes the room
// that container shows. Laid out at an unbounded height instead, it is as tall
// as all its rows, and reads that as the view: every row is built for a screen
// taller than any, and the list stops scrolling through them.
func TestListInScrollTakesTheRoomItShows(t *testing.T) {
	var s ListState
	tt := coreNewTester(func(c *context) {
		coreScroll(c).Grow(1).Children(func() {
			coreList(c, &s, 200, func(i int) {
				coreBox(c).Height(40).Children(func() { coreTextf(c, "row %d", i) })
			}).Grow(1)
		})
	}, 400, 300)
	tt.Frame()
	if _, last := s.Visible(); last > 20 {
		t.Errorf("shows rows 0 to %d: the list took the height of all its rows", last)
	}
	first0, _ := s.Visible()
	tt.Move(200, 150)
	for range 5 {
		tt.Scroll(200, 150, 0, 200)
	}
	tt.Frame()
	if first, _ := s.Visible(); first == first0 {
		t.Errorf("scrolling left row %d first", first)
	}
}

// BenchmarkFrameListInScroll measures a frame of a list of titled rows inside
// a scrolling ancestor, each a truncated title and a line of summary, as a
// file list or a table of documents.
func BenchmarkFrameListInScroll(b *testing.B) {
	for _, n := range []int{20, 740} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			title := "a document title that is cut short and then some more of it"
			var s ListState
			tt := coreNewTester(func(c *context) {
				coreScroll(c).Grow(1).Children(func() {
					coreList(c, &s, n, func(i int) {
						coreBox(c).Height(62).Children(func() {
							coreTextf(c, "%s %d", title, i)
							coreTextf(c, "a line of the summary under it, cut where the row ends")
						})
					}).Grow(1)
				})
			}, 860, 560)
			tt.Frame()
			b.ResetTimer()
			for b.Loop() {
				tt.Frame()
			}
		})
	}
}
