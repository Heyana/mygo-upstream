package backend

import (
	"github.com/egoist/mygo/internal/platform"
)

// Ime translates Android's InputConnection callbacks into the calls a
// platform.TextInputClient understands, and answers the queries an input
// method makes about the text around the caret.
//
// Android gives an input method an InputConnection and lets it edit through
// that: it sets and replaces a composing (preedit) region, commits it, ends
// it, deletes around the caret, and asks for the text before and after the
// caret. mygo describes the same editing in different words — marked text, an
// explicit replacement range, a selection in UTF-16 units — so this type is
// the whole translation between the two. It holds no text: the client owns
// the document, the selection and the marked range, exactly as
// docs/ui/text-input-client.md says it must. Holding none is also what makes
// this file testable on a desktop with no device and no JNI (textinput_test.go).
//
// # Ranges
//
// Android counts Java chars, which are UTF-16 code units, and platform.TextRange
// is defined in those same units, so offsets pass through this file unchanged.
// One emoji is one Java char and two code units on both sides, which is the
// case a byte-based or rune-based mapping would get wrong.
//
// # Threading
//
// Every method here calls into the client, whose callbacks the contract
// requires to run on the backend's main (pump) goroutine. The JNI entry
// points in android/jni/jni_textinput.go therefore run each call on the
// pump and wait for it, rather than calling the client on the Java thread
// that made the request.
type Ime struct {
	// client is the focused editable element's client, or nil when no
	// element is editing. TextInputClient values are pointers (the ui
	// contract requires it), so comparing one with another is safe.
	client platform.TextInputClient
	// comp turns "end the preedit, then commit" back into one replacement.
	// It is platform's own type rather than a second state machine here:
	// platform/text_input.go:41 explains the ordering it exists to
	// preserve.
	comp platform.InputComposition

	// OnActive, when set, is told whether an editable element has the
	// keyboard, so the host can raise or lower the soft keyboard. Only
	// package main can reach Java, so it is package main's init that
	// installs the function (jni_textinput.go); this package stays free of
	// JNI. It runs on the pump goroutine.
	OnActive func(active bool)
}

// theIme is the process's single state machine. Android has one Activity and
// one focused element at a time, so one is enough; a second instance would
// mean a second guess at which field the keyboard is for.
var theIme = &Ime{}

// TheIme returns the process's text-input state machine. The JNI entry
// points in package main and this package's Surface share this one value.
func TheIme() *Ime { return theIme }

// SetTextInput adopts a Surface's text-input state, as
// platform.Surface.SetTextInput describes it. It is what the Android Surface
// calls.
//
// Only Client is used: the Text/Start/End snapshot is the plain-widget and
// TextCaret fallback, and the Android backend has no SurfaceEvent path to
// deliver it through (the touch and key stubs in app.go are where one would
// go). A platform.TextInputState with no client therefore raises no keyboard
// — this backend could not type into it — which is why the keyboard sink is
// told "active and editable", not merely "active".
func (m *Ime) SetTextInput(t platform.TextInputState) {
	if !t.Active {
		m.SetClient(nil)
		m.active(false)
		return
	}
	m.SetClient(t.Client)
	m.active(t.Client != nil)
}

// SetClient points the state machine at the focused element's client, or at
// nothing when the element stops editing.
//
// Changing the client ends whatever preedit the old one held, as the ui
// contract asks ("Changing the client resets native preedit so a candidate
// cannot move to a newly focused field"): the old editor keeps the text its
// preedit had become, but no longer calls it composing, so a commit that
// arrives late cannot mark text in a field that no longer has the keyboard.
func (m *Ime) SetClient(c platform.TextInputClient) {
	if m.client == c {
		return
	}
	if m.client != nil {
		if _, marked := m.client.MarkedRange(); marked {
			m.client.UnmarkText()
		}
	}
	m.client = c
	m.comp.Reset()
}

// active reports the keyboard's state to the host, if one is installed.
func (m *Ime) active(on bool) {
	if m.OnActive != nil {
		m.OnActive(on)
	}
}

// Client returns the client the state machine is translating for, or nil.
// The JNI layer reads the selection through Selection and the text through
// the two cursor queries, not through this.
func (m *Ime) Client() platform.TextInputClient { return m.client }

// SetComposingText is Android's setComposingText: replace the composing
// region — the selection when nothing is composing — with text, and mark the
// result as composing, putting the caret inside it.
//
// A Chinese input method calls this once per keystroke, so the marked range
// it replaces is the one the call before it marked: the preedit grows in
// place instead of being committed letter by letter. That is also why the
// pending range of an earlier finishComposingText is dropped first — the
// marking edit supersedes it, and a later commit must land on this preedit
// rather than on the one that already ended (internal/windows does the same
// reset for the same reason).
//
// newCursorPosition is in Java chars: the caret is at the end of the inserted
// text plus newCursorPosition-1 when that is positive, and at its start plus
// newCursorPosition otherwise. TextInputClient expresses a position inside
// marked text directly, so nothing has to be corrected afterwards.
func (m *Ime) SetComposingText(text string, newCursorPosition int) bool {
	c := m.client
	if c == nil {
		return false
	}
	m.comp.Reset()
	var r *platform.TextRange
	if marked, ok := c.MarkedRange(); ok {
		r = &marked
	}
	caret := preeditCaret(text, newCursorPosition)
	c.SetMarkedText(r, text, platform.TextRange{Start: caret, End: caret})
	return true
}

// preeditCaret converts Android's newCursorPosition into an offset inside
// freshly marked text, clamped to the marked text: a position outside it
// would be a caret in text the input method has not committed yet.
func preeditCaret(text string, newCursorPosition int) int {
	n := platform.UTF16Len(text)
	if newCursorPosition > 0 {
		return min(n+newCursorPosition-1, n)
	}
	return min(max(newCursorPosition, 0), n)
}

// CommitText is Android's commitText: replace the composing region — or the
// selection when nothing is composing — with text, and end the composition.
//
// The nil range is deliberate and is the one place platform.InputComposition
// is used, not merely borrowed: some input methods end the preedit before
// they deliver its result, so the range to replace is no longer marked by the
// time the commit arrives. InputComposition remembers the range it unmarked
// and substitutes it for the nil, which turns "unmark, then commit" into the
// single replacement the client expects. Writing that ordering here again
// would be a second copy of a decision the platform already made.
func (m *Ime) CommitText(text string, newCursorPosition int) bool {
	c := m.client
	if c == nil {
		return false
	}
	m.comp.Replace(c, nil, text)
	m.placeCaret(c, text, newCursorPosition)
	return true
}

// placeCaret puts the caret where Android asked, when the client's own choice
// differs from it.
//
// ReplaceText leaves the caret after the text it inserted, which is exactly
// newCursorPosition 1 — what nearly every commit asks for, and the only
// position the contract can state — so anything else is worth one more edit.
// The wanted position is counted from the caret the client just chose, which
// is the end of what it inserted, so no range has to be guessed: a preedit
// that was unmarked before the commit leaves nothing to guess from, and this
// does not try.
//
// The extra edit is a replacement of an empty range with no text:
// TextInputClient has no SetSelection, and this is the one edit that moves the
// caret and changes nothing else.
func (m *Ime) placeCaret(c platform.TextInputClient, text string, newCursorPosition int) {
	end := c.Selection().Caret()
	want := end
	if newCursorPosition > 0 {
		want = end + newCursorPosition - 1
	} else {
		want = end - platform.UTF16Len(text) + newCursorPosition
	}
	if want = max(0, want); want != end {
		m.moveCaret(c, want)
	}
}

// FinishComposingText is Android's finishComposingText: the text stays where
// it is, the composing state ends.
//
// InputComposition.End is that, plus the one thing that has to survive it —
// the range it unmarked, so a commit that follows lands on the preedit
// instead of after it (platform/text_input.go:46).
func (m *Ime) FinishComposingText() bool {
	c := m.client
	if c == nil {
		return false
	}
	m.comp.End(c)
	return true
}

// DeleteSurroundingText is Android's deleteSurroundingText: delete
// beforeLength Java chars before the caret and afterLength after it.
//
// The range is clamped to the document, so an input method that asks for more
// text than exists deletes what does — this is the backspace a Japanese or
// Hangul input method sends while composing, and the range it deletes from is
// the caret the client reports, which inside marked text is the position
// inside the preedit.
func (m *Ime) DeleteSurroundingText(beforeLength, afterLength int) bool {
	c := m.client
	if c == nil {
		return false
	}
	// An unrelated edit invalidates a pending end: the range it remembered
	// may already have been deleted, and a later commit would target text
	// that is no longer there.
	m.comp.Reset()
	caret := max(0, c.Selection().Caret())
	r := platform.NormalizeTextRange(platform.TextRange{
		Start: caret - max(0, beforeLength),
		End:   caret + max(0, afterLength),
	})
	if r.Start == r.End {
		// Nothing to delete is not a failure: the text is editable, and the
		// input method only asked for more context than there is.
		return true
	}
	c.ReplaceText(&r, "")
	return true
}

// SetSelection is Android's setSelection: put the caret at a position, or
// select a range for a candidate to replace.
//
// A collapsed request is a zero-length replacement, the same edit placeCaret
// uses, and it is skipped when the caret is already there so that no
// needless mutation reaches the client (ReplaceText also clears preedit, and
// an input method that re-states the caret it already had must not end the
// preedit it is in the middle of).
//
// A request that selects a range is reported as not performed instead of
// being faked. TextInputClient can only replace a range with text, and
// replacing one with the empty string deletes it — which is precisely what
// selecting it must not do. Android's contract says to return false when the
// action was not taken, and a false is the only answer here that does not
// destroy the text the input method asked to select. This is a contract gap,
// not a translation gap: a client-side SetSelection would close it (see the
// task's output).
func (m *Ime) SetSelection(start, end int) bool {
	c := m.client
	if c == nil {
		return false
	}
	start, end = max(0, start), max(0, end)
	if start > end {
		start, end = end, start
	}
	if start != end {
		return false
	}
	if c.Selection().Caret() == start {
		return true
	}
	m.comp.Reset()
	m.moveCaret(c, start)
	return true
}

// moveCaret moves the caret with the one edit the contract allows it: a
// replacement of an empty range with no text. A client replaces nothing and
// sets nothing, so the caret lands there and the text is untouched.
func (m *Ime) moveCaret(c platform.TextInputClient, at int) {
	r := platform.TextRange{Start: at, End: at}
	c.ReplaceText(&r, "")
}

// TextBeforeCursor is Android's getTextBeforeCursor: up to n Java chars
// before the caret, as an input method sees them — preedit included, because
// preedit is a virtual insertion into the document until it is committed.
//
// The second result is whether there is a client to ask at all; the returned
// text is empty when the caret is at the start of the document, which is a
// window, not an absence.
func (m *Ime) TextBeforeCursor(n int) (string, bool) {
	c := m.client
	if c == nil {
		return "", false
	}
	if n <= 0 {
		return "", true
	}
	caret := max(0, c.Selection().Caret())
	text, _ := c.TextForRange(platform.TextRange{Start: max(0, caret-n), End: caret})
	return text, true
}

// TextAfterCursor is Android's getTextAfterCursor: up to n Java chars after
// the caret, preedit included, with the same empty-window rule as
// TextBeforeCursor.
func (m *Ime) TextAfterCursor(n int) (string, bool) {
	c := m.client
	if c == nil {
		return "", false
	}
	if n <= 0 {
		return "", true
	}
	caret := max(0, c.Selection().Caret())
	text, _ := c.TextForRange(platform.TextRange{Start: caret, End: caret + n})
	return text, true
}

// SelectedText is Android's getSelectedText: the selected text, and whether
// anything is selected. Android's method answers null for a collapsed
// selection, so the bool is the caller's null: a client that reports no
// selection is not a client that failed to answer.
func (m *Ime) SelectedText() (string, bool) {
	c := m.client
	if c == nil {
		return "", false
	}
	r := platform.NormalizeTextRange(c.Selection().Range)
	if r.Start == r.End {
		return "", false
	}
	text, _ := c.TextForRange(r)
	return text, true
}

// Selection is the caret and the selection Android needs for
// EditorInfo.initialSelStart/End and for the Connection's own getSelectedText
// bookkeeping. The third result is whether a client is focused at all: an
// input method reading a position from a field with nothing focused would
// anchor its candidate window to a place that does not exist.
func (m *Ime) Selection() (start, end int, ok bool) {
	c := m.client
	if c == nil {
		return 0, 0, false
	}
	r := platform.NormalizeTextRange(c.Selection().Range)
	return r.Start, r.End, true
}
