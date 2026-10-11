package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

func TestComboboxInlineChoiceAndFocus(t *testing.T) {
	for _, action := range []string{"first", "arrows", "click"} {
		t.Run(action, func(t *testing.T) {
			query, picked, count := "", "", 0
			tt := NewTester(func(c *Context) {
				p := ComboboxBase(c, &query)
				p.Input.Label("Search").AutoFocus()
				p.Inline(func(panel Element) {
					panel.FillWidth()
					for _, value := range []string{"Alpha", "Beta"} {
						p.Item(value).Height(30).Children(func() { Text(c, value) })
					}
				})
				if value, ok := p.Chosen(); ok {
					picked, count = value, count+1
				}
			}, 300, 200)
			want := "Alpha"
			switch action {
			case "arrows":
				tt.Key(0, KeyDown)
				tt.Key(0, KeyUp)
				tt.Key(0, KeyDown)
				want = "Beta"
			case "click":
				if err := tt.Click("Beta"); err != nil {
					t.Fatal(err)
				}
				want = "Beta"
			}
			if action != "click" {
				tt.Key(0, KeyEnter)
			}
			tt.Frame()
			if picked != want || count != 1 || !tt.Focused("Search") {
				t.Fatalf("picked %q, count %d, focused %v", picked, count, tt.Focused("Search"))
			}
		})
	}
}

func TestComboboxInlineEmptyAndFiltering(t *testing.T) {
	query, picked := "", ""
	tt := NewTester(func(c *Context) {
		p := ComboboxBase(c, &query)
		p.Input.Label("Search").AutoFocus()
		p.Inline(func(panel Element) {
			p.Input.Changed() // apply the query before filtering this frame
			found := false
			for _, value := range []string{"Alpha", "Beta"} {
				if strings.Contains(strings.ToLower(value), strings.ToLower(query)) {
					found = true
					p.Item(value).Height(30).Children(func() { Text(c, value) })
				}
			}
			if !found {
				Text(c, "No matches")
			}
		})
		if value, ok := p.Chosen(); ok {
			picked = value
		}
	}, 300, 200)
	tt.Type("zz")
	if !tt.HasText("No matches") {
		t.Fatal("empty message hidden")
	}
	tt.Key(0, KeyEnter)
	if picked != "" {
		t.Fatal("empty list chose an old option")
	}
	tt.Key(Cmd, KeyA)
	tt.Type("beta")
	tt.Key(0, KeyEnter)
	if picked != "Beta" {
		t.Fatalf("filtered choice %q", picked)
	}
}

func TestComboboxInlineEscapeClosesContainingDialog(t *testing.T) {
	open, query := true, ""
	tt := NewTester(func(c *Context) {
		Modal(c, &open, func() {
			p := ComboboxBase(c, &query)
			p.Input.Label("Search").AutoFocus()
			p.Inline(func(panel Element) {
				p.Item("Alpha").Children(func() { Text(c, "Alpha") })
			})
			p.Chosen()
			Button(c, "Other")
		})
	}, 400, 300)
	tt.Key(0, KeyTab)
	if !tt.HasText("Alpha") || !tt.Focused("Other") {
		t.Fatal("inline options closed when the field lost focus")
	}
	tt.Key(0, KeyEscape)
	if open || tt.HasText("Alpha") {
		t.Fatal("Escape did not close the containing dialog")
	}
}

func TestComboboxInlineScrollAndAccessibility(t *testing.T) {
	query := ""
	tt := NewTester(func(c *Context) {
		p := ComboboxBase(c, &query)
		p.Input.Label("Search").AutoFocus()
		p.Inline(func(panel Element) {
			Scroll(c).Height(120).Children(func() {
				for i := range 40 {
					value := fmt.Sprintf("Option %02d", i)
					p.Item(value).Height(24).Children(func() { Text(c, value) })
				}
			})
		})
		p.Chosen()
	}, 300, 220)
	tt.send(platform.SurfaceEvent{Kind: platform.AccessibilityOn})
	f, ok := byID(tt.h.access, tt.h.access.Focus)
	if !ok || f.Label != "Option 00" || f.PosInSet != 1 || f.SetSize != 40 {
		t.Fatalf("initial accessible choice %+v", f)
	}
	first, _ := tt.Find("Option 00")
	x, y := first.X+first.W/2, first.Y+first.H/2
	tt.Move(x, y)
	for range 15 {
		tt.Scroll(x, y, 0, 120)
	}
	last, _ := tt.Find("Option 39")
	if last.H == 0 || last.Y > first.Y+120 {
		t.Fatalf("wheel pulled back to highlight: %v", last)
	}
	tt.Key(0, KeyDown)
	next, _ := tt.Find("Option 01")
	if next.Y < first.Y || next.Y+next.H > first.Y+120 {
		t.Fatalf("arrow did not reveal next option: %v", next)
	}
}
