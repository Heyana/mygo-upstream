package terminal

import "github.com/egoist/mygo/plugins/terminal/internal/vt"

type readingPosition struct {
	offset, distance int
	absolute         bool
}

func readingAt(screen *vt.Terminal) *readingPosition {
	if screen == nil || screen.AtBottom() {
		return nil
	}
	bar := screen.Scrollbar()
	return &readingPosition{offset: int(bar.Offset), distance: int(bar.Total - bar.Offset - bar.Len)}
}

func (p *readingPosition) restore(screen *vt.Terminal) {
	if p == nil || screen == nil {
		return
	}
	if p.absolute {
		screen.ScrollToRow(p.offset)
	} else {
		screen.ScrollToBottom()
		screen.ScrollBy(-p.distance)
	}
}

// Replace a complete snapshot supplied by Conn in one critical section,
// preserving the reading position when source history and geometry allow it.
func (t *Terminal) restoreSnapshot(data []byte, cols, rows int) {
	t.clearInputContext()
	t.mu.Lock()
	if t.term == nil || t.closed {
		t.mu.Unlock()
		return
	}
	screen := t.term
	position := readingAt(screen)
	oldTotal := int(t.term.Scrollbar().Total)
	oldCols, oldRows := t.size.cols, t.size.rows
	if cols > 0 && rows > 0 && t.opts.FixedCols > 0 && t.opts.FixedRows > 0 {
		t.opts.FixedCols, t.opts.FixedRows = cols, rows
		size := t.size
		size.cols, size.rows = cols, rows
		t.resize(size)
	}
	t.term.Reset()
	t.term.Write(data)
	if position != nil {
		newTotal := int(t.term.Scrollbar().Total)
		// With retained history and unchanged source geometry, appended output
		// must not push the reader toward newer content.
		position.absolute = newTotal >= oldTotal && oldCols == t.size.cols && oldRows == t.size.rows
		position.restore(t.term)
	}
	ev := t.events
	t.events = events{}
	title := ""
	if ev.title {
		title = t.term.Title()
	}
	t.mu.Unlock()
	t.fire(ev, title)
	t.redraw()
}
