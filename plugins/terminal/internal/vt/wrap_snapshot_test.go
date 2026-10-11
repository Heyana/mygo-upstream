package vt

import (
	"strings"
	"testing"
)

func TestVTSnapshotPreservesSoftWrapAndHyperlinks(t *testing.T) {
	loadLib(t)
	for _, osc := range []bool{false, true} {
		source, err := NewTerminal(80, 20, Effects{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(source.Free)
		url := "https://example.com/" + strings.Repeat("a", 35)
		visible := url
		content := url
		if osc {
			visible = strings.Repeat("label", 8)
			content = "\x1b]8;;" + url + "\x1b\\" + visible + "\x1b]8;;\x1b\\"
		}
		source.Write([]byte(strings.Repeat("padding ", 8) + content + "\r\nhard boundary"))
		{
			clone, err := NewTerminal(80, 20, Effects{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(clone.Free)
			snapshot := source.VT()
			clone.Write(snapshot)
			if !clone.RowsJoined(0) || clone.Text() != source.Text() {
				t.Fatal("restoring VT changed soft wraps or terminal text")
			}
			clone.Resize(32, 20, 8, 16)
			if !strings.Contains(clone.Text(), visible+"\nhard boundary") {
				t.Fatalf("reflow split the link or joined a hard newline: %q", clone.Text())
			}
			if osc {
				ref, ok := clone.CellAt(2, 2)
				if !ok || clone.Hyperlink(ref) != url {
					t.Fatal("reflow lost the OSC 8 link target")
				}
			}
		}
	}
}

func TestVTSnapshotKeepsRepeatedLabelsAndHistoryLinkTargets(t *testing.T) {
	loadLib(t)
	source, err := NewTerminal(80, 20, Effects{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Free()
	for i := 0; i < 160; i++ {
		url := "https://example.com/" + strings.Repeat("a", i+1) + "?x=1&y=2"
		source.Write([]byte("\x1b[1;34m\x1b]8;;" + url + "\x1b\\中文🙂 label\x1b]8;;\x1b\\\x1b[0m\r\n"))
	}
	clone, err := NewTerminal(80, 20, Effects{})
	if err != nil {
		t.Fatal(err)
	}
	defer clone.Free()
	clone.Write(source.VT())
	if clone.Text() != source.Text() {
		t.Fatal("snapshot changed repeated labels or history")
	}
	for _, offset := range []int{0, 40, 100} {
		source.ScrollToRow(offset)
		clone.ScrollToRow(offset)
		for row := 0; row < 20; row++ {
			a, okA := source.CellAt(0, row)
			b, okB := clone.CellAt(0, row)
			if !okA || !okB || source.Hyperlink(a) == "" || clone.Hyperlink(b) != source.Hyperlink(a) {
				t.Fatalf("history link target lost at %d/%d", offset, row)
			}
		}
	}
}

func TestSnapshotLinksLeavesMismatchedTextUnchanged(t *testing.T) {
	data := []byte("\x1b[Hone label")
	markup := []byte(`<div><a href="https://wrong.example">other label</a></div>`)
	if string(snapshotLinks(data, markup)) != string(data) {
		t.Fatal("mismatched HTML assigned a guessed link")
	}
}

func TestVTSnapshotKeepsLinkUnderPendingWrapCursor(t *testing.T) {
	loadLib(t)
	source, err := NewTerminal(80, 20, Effects{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Free()
	source.Write([]byte("\x1b]8;;https://example.com/\x1b\\" + strings.Repeat("a", 80) + "\x1b]8;;\x1b\\"))
	clone, err := NewTerminal(80, 20, Effects{})
	if err != nil {
		t.Fatal(err)
	}
	defer clone.Free()
	clone.Write(source.VT())
	a, _ := source.CellAt(79, 0)
	b, _ := clone.CellAt(79, 0)
	if source.Hyperlink(a) != clone.Hyperlink(b) {
		t.Fatal("cursor restoration erased the final linked cell")
	}
}
