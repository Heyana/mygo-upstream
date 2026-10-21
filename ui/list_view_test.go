package ui

import "testing"

// A list inside a scrolling ancestor is as tall as its rows: the ancestor
// scrolls the content, so nothing holds the list to a screen. Read as the
// view, that height places and builds every row — this fails with all 1000
// built for a 400-high window of 40-high rows.
func TestListInScrollingAncestorBuildsOnlyItsView(t *testing.T) {
	var s ListState
	built := map[int]bool{}
	tt := coreNewTester(func(c *context) {
		coreColumn(c).Grow(1).Children(func() {
			coreScroll(c).Grow(1).Children(func() {
				coreList(c, &s, 1000, func(i int) {
					built[i] = true
					coreBox(c).Height(40).Children(func() { coreTextf(c, "row %d", i) })
				})
			})
		})
	}, 300, 400)
	tt.Frame()
	built = map[int]bool{}
	tt.Frame()
	if built[500] {
		t.Error("row 500 was built: the list placed all 1000 rows, not the ones its view shows")
	}
	if len(built) > 40 {
		t.Errorf("%d rows built for a 400-high window of 40-high rows", len(built))
	}
}

// The rows the ancestor shows are the ones built, at any offset: reading its
// height alone builds rows 0..11 forever, and the view goes blank once the
// ancestor scrolls past them.
func TestListInScrollingAncestorBuildsTheRowsItShows(t *testing.T) {
	var s ListState
	built := map[int]bool{}
	tt := coreNewTester(func(c *context) {
		coreColumn(c).Grow(1).Children(func() {
			coreScroll(c).Grow(1).Children(func() {
				coreList(c, &s, 1000, func(i int) {
					built[i] = true
					coreBox(c).Height(40).Children(func() { coreTextf(c, "row %d", i) })
				})
			})
		})
	}, 300, 400)
	tt.Frame()
	// 50 × 400 scrolls 20000 of the 40000 the 1000 rows take: rows 500 on show.
	for i := 0; i < 50; i++ {
		tt.Scroll(150, 200, 0, 400)
		tt.Frame()
	}
	tt.Frame()
	built = map[int]bool{}
	tt.Frame()
	for _, i := range []int{500, 505, 509} {
		if !built[i] {
			t.Fatalf("row %d is not built, though the ancestor shows it", i)
		}
	}
	if len(built) > 40 {
		t.Errorf("%d rows built for a 400-high window of 40-high rows", len(built))
	}
	// Built is not enough: 20000 scrolled puts row 500, the first one the
	// ancestor shows, at the top of its window.
	if r, ok := tt.Find("row 500"); !ok || r.Y < -0.5 || r.Y > 0.5 {
		t.Errorf("row 500 shows at y=%v, not at the top of the window", r.Y)
	}
}

// The list's place in the ancestor's content counts: a box above it holds its
// rows 20 down from the content's top, so 20000 scrolled shows row 500 20
// below the window's top, and the rows before it stay built.
func TestListInScrollingAncestorCountsItsPlace(t *testing.T) {
	var s ListState
	built := map[int]bool{}
	tt := coreNewTester(func(c *context) {
		coreColumn(c).Grow(1).Children(func() {
			coreBox(c).Height(60)
			coreScroll(c).Grow(1).Children(func() {
				coreBox(c).Height(20)
				coreList(c, &s, 1000, func(i int) {
					built[i] = true
					coreBox(c).Height(40).Children(func() { coreTextf(c, "row %d", i) })
				})
			})
		})
	}, 300, 400)
	tt.Frame()
	for i := 0; i < 50; i++ {
		tt.Scroll(150, 200, 0, 400)
		tt.Frame()
	}
	tt.Frame()
	built = map[int]bool{}
	tt.Frame()
	if !built[500] || built[0] {
		t.Fatalf("rows built run from %d: the list read the band from its content, not the ancestor's", 0)
	}
	// The scrolling ancestor starts at 60, and the box above the list puts
	// row 500 20 into its window.
	if r, ok := tt.Find("row 500"); !ok || r.Y < 79.5 || r.Y > 80.5 {
		t.Errorf("row 500 shows at y=%v, not 20 into the window that starts at 60", r.Y)
	}
}

// Scrolling the ancestor back up shows the rows above again: the band moves
// with it, and the list reads it either way.
func TestListInScrollingAncestorShowsRowsAgainWhenItScrollsBack(t *testing.T) {
	var s ListState
	built := map[int]bool{}
	tt := coreNewTester(func(c *context) {
		coreColumn(c).Grow(1).Children(func() {
			coreScroll(c).Grow(1).Children(func() {
				coreList(c, &s, 1000, func(i int) {
					built[i] = true
					coreBox(c).Height(40).Children(func() { coreTextf(c, "row %d", i) })
				})
			})
		})
	}, 300, 400)
	tt.Frame()
	for i := 0; i < 50; i++ {
		tt.Scroll(150, 200, 0, 400)
		tt.Frame()
	}
	for i := 0; i < 50; i++ {
		tt.Scroll(150, 200, 0, -400)
		tt.Frame()
	}
	tt.Frame()
	built = map[int]bool{}
	tt.Frame()
	if !built[0] || built[500] {
		t.Fatalf("rows built run to %d, not back to the top the ancestor shows", len(built))
	}
	if r, ok := tt.Find("row 0"); !ok || r.Y < -0.5 || r.Y > 0.5 {
		t.Errorf("row 0 shows at y=%v, not at the top of the window", r.Y)
	}
}
