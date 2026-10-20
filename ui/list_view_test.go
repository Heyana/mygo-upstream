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
