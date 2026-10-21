package ui

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkFrameListInScrollScrolled measures the frame where the band the
// ancestor shows is not the list's top: the list reads which rows it shows
// from the ancestor's offset, every frame.
func BenchmarkFrameListInScrollScrolled(b *testing.B) {
	for _, n := range []int{740} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			title := strings.Repeat("a document title that is cut short ", 3)
			var s ListState
			tt := coreNewTester(func(c *context) {
				coreColumn(c).Grow(1).Children(func() {
					coreScroll(c).Grow(1).Children(func() {
						coreList(c, &s, n, func(i int) {
							coreBox(c).Height(62).Children(func() {
								coreTextf(c, "%s %d", title, i)
								coreTextf(c, "a line of the summary under it, cut where the row ends")
							})
						})
					})
				})
			}, 860, 560)
			tt.Frame()
			// Halfway into the 62-high rows of n.
			for i := 0; i < (n*62/2)/400; i++ {
				tt.Scroll(430, 280, 0, 400)
				tt.Frame()
			}
			tt.Frame()
			b.ResetTimer()
			for b.Loop() {
				tt.Frame()
			}
		})
	}
}
