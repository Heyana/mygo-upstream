//go:build windows && (amd64 || arm64)

package windows

import "testing"

// TestTrayNumber numbers icons by their IDs, whatever order they come in,
// apart from the numbers NewTray counts.
func TestTrayNumber(t *testing.T) {
	number := func(ids ...string) map[string]uint32 {
		b := &Backend{trays: map[uint32]*tray{}}
		out := map[string]uint32{}
		for _, id := range ids {
			b.nextTray++
			tr := &tray{b: b, id: b.nextTray}
			b.trays[tr.id] = tr
			tr.SetID(id)
			if b.trays[tr.id] != tr || len(b.trays) != int(b.nextTray) {
				t.Fatalf("%q: the icons are %v", id, b.trays)
			}
			out[id] = tr.id
		}
		return out
	}
	a, b := number("app.cpu", "app.memory"), number("app.memory", "app.cpu")
	for id, n := range a {
		if b[id] != n {
			t.Errorf("%q is %#x, then %#x", id, n, b[id])
		}
		if n&(1<<31) == 0 {
			t.Errorf("%q is %#x, which NewTray may count to", id, n)
		}
	}
	if a["app.cpu"] == a["app.memory"] {
		t.Error("two IDs have one number")
	}
	// Without an ID, or with one another icon has, an icon keeps its own.
	if n := number("")[""]; n != 1 {
		t.Errorf("an icon without an ID is %#x", n)
	}
	if twice := number("app.cpu", "app.cpu"); twice["app.cpu"] != 2 {
		t.Errorf("an icon with the ID of another is %#x", twice["app.cpu"])
	}
}
