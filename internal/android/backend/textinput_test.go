package backend

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

// recordingClient is a TextInputClient over a plain string: the whole client
// contract in one place, so the state machine can be tested on a desktop with
// no device, no JNI and no package ui.
//
// It keeps the marked text inside the document, as the contract says a client
// should ("Preedit is a virtual insertion into the native document until
// committed"), and it records every call it receives. The recorded sequence,
// not just the resulting text, is what most of these tests assert: which range
// a commit landed on, and whether an unmark preceded it, are invisible in the
// document afterwards — and the unmark-then-commit ordering is the whole
// reason platform.InputComposition exists.
type recordingClient struct {
	text string
	sel  platform.TextSelection
	mark *platform.TextRange
	ops  []string
}

// target is the range a nil replacement means, as the contract defines it:
// the preedit if there is one, the selection otherwise.
func (c *recordingClient) target(r *platform.TextRange) platform.TextRange {
	if r != nil {
		return platform.NormalizeTextRange(*r)
	}
	if c.mark != nil {
		return platform.NormalizeTextRange(*c.mark)
	}
	return platform.NormalizeTextRange(c.sel.Range)
}

func (c *recordingClient) TextForRange(r platform.TextRange) (string, platform.TextRange) {
	n := platform.UTF16Len(c.text)
	r = platform.NormalizeTextRange(r)
	a, z := min(r.Start, n), min(r.End, n)
	ba, bz := platform.UTF16ByteOffset(c.text, a), platform.UTF16ByteOffset(c.text, z)
	return c.text[ba:bz], platform.TextRange{
		Start: platform.UTF16Len(c.text[:ba]),
		End:   platform.UTF16Len(c.text[:bz]),
	}
}

func (c *recordingClient) Selection() platform.TextSelection { return c.sel }

func (c *recordingClient) MarkedRange() (platform.TextRange, bool) {
	if c.mark != nil {
		return *c.mark, true
	}
	return platform.TextRange{}, false
}

func (c *recordingClient) ReplaceText(r *platform.TextRange, text string) {
	wanted := c.target(r)
	ba, bz := platform.UTF16ByteOffset(c.text, wanted.Start), platform.UTF16ByteOffset(c.text, wanted.End)
	c.text = c.text[:ba] + text + c.text[bz:]
	end := platform.UTF16Len(c.text[:ba]) + platform.UTF16Len(text)
	c.sel = platform.TextSelection{Range: platform.TextRange{Start: end, End: end}}
	c.mark = nil
	c.ops = append(c.ops, fmt.Sprintf("replace(%d,%d,%s)", wanted.Start, wanted.End, opText(text)))
}

func (c *recordingClient) SetMarkedText(r *platform.TextRange, text string, selected platform.TextRange) {
	wanted := c.target(r)
	ba, bz := platform.UTF16ByteOffset(c.text, wanted.Start), platform.UTF16ByteOffset(c.text, wanted.End)
	c.text = c.text[:ba] + text + c.text[bz:]
	start := platform.UTF16Len(c.text[:ba])
	n := platform.UTF16Len(text)
	selected = platform.NormalizeTextRange(selected)
	selected.Start, selected.End = min(selected.Start, n), min(selected.End, n)
	c.sel = platform.TextSelection{Range: platform.TextRange{Start: start + selected.Start, End: start + selected.End}}
	if text == "" {
		// The built-in widgets report no marked range for empty preedit, and
		// so does this (ui/editor_input.go:165).
		c.mark = nil
	} else {
		mark := platform.TextRange{Start: start, End: start + n}
		c.mark = &mark
	}
	c.ops = append(c.ops, fmt.Sprintf("mark(%d,%d,%s,%d)", wanted.Start, wanted.End, opText(text), selected.Start))
}

func (c *recordingClient) UnmarkText() {
	c.mark = nil
	c.ops = append(c.ops, "unmark")
}

// BoundsForRange and IndexForPoint answer "no geometry": the state machine
// never asks for any (placing the candidate window is the Surface's job), and
// a client that has to implement them to be a client can say so honestly.
func (c *recordingClient) BoundsForRange(platform.TextRange) (platform.RectF, platform.TextRange, bool) {
	return platform.RectF{}, platform.TextRange{}, false
}

func (c *recordingClient) IndexForPoint(float64, float64) (int, bool) { return 0, false }

// opText keeps the call log readable when a test commits a very long string.
func opText(s string) string {
	if n := platform.UTF16Len(s); n > 32 {
		return fmt.Sprintf("%q", fmt.Sprintf("<%d chars>", n))
	}
	return fmt.Sprintf("%q", s)
}

// markedUnits is the marked range's length, or -1 when nothing is marked.
func (c *recordingClient) markedUnits() int {
	if r, ok := c.MarkedRange(); ok {
		return r.End - r.Start
	}
	return -1
}

// TestTextInput pins the translation of Android's InputConnection editing
// callbacks onto platform.TextInputClient.
//
// The cases are the ones an input method actually produces: an English key
// commits directly, a Chinese one composes a candidate keystroke by keystroke
// and commits once, a backspace during composition shrinks the preedit or
// deletes around the caret, a composition is cancelled, empty and very long
// text arrive, the caret is moved and read, and — the case that costs a bug
// when it is missing — the preedit is ended before its result is committed.
func TestTextInput(t *testing.T) {
	tests := []struct {
		name string
		text string
		// caret is the initial caret; sel, when set, is an initial
		// selection such as a pointer drag would leave.
		caret int
		sel   *[2]int
		calls func(t *testing.T, m *Ime)
		want  string
		// wantCaret is in UTF-16 code units, as Android counts them.
		wantCaret  int
		wantMarked int // marked range length, or -1 for none
		wantOps    []string
	}{
		{
			name: "english direct commit",
			calls: func(t *testing.T, m *Ime) {
				if !m.CommitText("hello", 1) {
					t.Error("CommitText reported no client")
				}
			},
			want:       "hello",
			wantCaret:  5,
			wantMarked: -1,
			wantOps:    []string{`replace(0,0,"hello")`},
		},
		{
			name: "chinese preedit keystroke by keystroke, then one commit",
			calls: func(t *testing.T, m *Ime) {
				for _, k := range []string{"n", "ni", "nih", "nihao"} {
					if !m.SetComposingText(k, 1) {
						t.Fatalf("SetComposingText(%q) reported no client", k)
					}
				}
				if !m.CommitText("你好", 1) {
					t.Error("CommitText reported no client")
				}
			},
			want:       "你好",
			wantCaret:  2,
			wantMarked: -1,
			wantOps: []string{
				`mark(0,0,"n",1)`,
				`mark(0,1,"ni",2)`,
				`mark(0,2,"nih",3)`,
				`mark(0,3,"nihao",5)`,
				`replace(0,5,"你好")`,
			},
		},
		{
			name: "backspace during preedit shrinks it in place",
			calls: func(t *testing.T, m *Ime) {
				m.SetComposingText("nihao", 1)
				m.SetComposingText("niha", 1)
			},
			// The preedit is still the document's text and still marked, and
			// the caret is inside it: the next keystroke replaces "niha".
			want:       "niha",
			wantCaret:  4,
			wantMarked: 4,
			wantOps:    []string{`mark(0,0,"nihao",5)`, `mark(0,5,"niha",4)`},
		},
		{
			name: "deleteSurroundingText deletes the preedit's last unit",
			calls: func(t *testing.T, m *Ime) {
				m.SetComposingText("nihao", 1)
				if !m.DeleteSurroundingText(1, 0) {
					t.Error("DeleteSurroundingText reported no client")
				}
			},
			// The caret is inside the preedit, so "one unit before it" is one
			// preedit character — which is how a Japanese input method
			// backspaces.
			want:       "niha",
			wantCaret:  4,
			wantMarked: -1,
			wantOps:    []string{`mark(0,0,"nihao",5)`, `replace(4,5,"")`},
		},
		{
			name: "preedit cancelled with empty composing text",
			calls: func(t *testing.T, m *Ime) {
				m.SetComposingText("nihao", 1)
				m.SetComposingText("", 1)
			},
			want:       "",
			wantCaret:  0,
			wantMarked: -1,
			wantOps:    []string{`mark(0,0,"nihao",5)`, `mark(0,5,"",0)`},
		},
		{
			name:       "empty commit changes nothing",
			text:       "hello",
			caret:      5,
			calls:      func(t *testing.T, m *Ime) { m.CommitText("", 1) },
			want:       "hello",
			wantCaret:  5,
			wantMarked: -1,
			wantOps:    []string{`replace(5,5,"")`},
		},
		{
			name: "empty commit deletes the preedit",
			calls: func(t *testing.T, m *Ime) {
				m.SetComposingText("ni", 1)
				m.CommitText("", 1)
			},
			want:       "",
			wantCaret:  0,
			wantMarked: -1,
			wantOps:    []string{`mark(0,0,"ni",2)`, `replace(0,2,"")`},
		},
		{
			name: "surrogate pair counts two units, as Android counts it",
			calls: func(t *testing.T, m *Ime) {
				if !m.CommitText("😀", 1) {
					t.Error("CommitText reported no client")
				}
			},
			want:       "😀",
			wantCaret:  2,
			wantMarked: -1,
			wantOps:    []string{`replace(0,0,"😀")`},
		},
		{
			name: "a very long commit arrives whole",
			calls: func(t *testing.T, m *Ime) {
				if !m.CommitText(strings.Repeat("字", 20000), 1) {
					t.Error("CommitText reported no client")
				}
			},
			want:       strings.Repeat("字", 20000),
			wantCaret:  20000,
			wantMarked: -1,
			wantOps:    []string{`replace(0,0,"<20000 chars>")`},
		},
		{
			name:  "caret moves and the cursor queries follow it",
			text:  "abcdef",
			caret: 6,
			calls: func(t *testing.T, m *Ime) {
				if !m.SetSelection(0, 0) {
					t.Error("SetSelection(0,0) reported no client")
				}
				if before, ok := m.TextBeforeCursor(3); !ok || before != "" {
					t.Errorf("at the start: before=%q ok=%v, want \"\" true", before, ok)
				}
				if after, _ := m.TextAfterCursor(3); after != "abc" {
					t.Errorf("after=%q, want \"abc\"", after)
				}
				if !m.SetSelection(6, 6) {
					t.Error("SetSelection(6,6) reported no client")
				}
				if before, _ := m.TextBeforeCursor(3); before != "def" {
					t.Errorf("at the end: before=%q, want \"def\"", before)
				}
				if _, ok := m.SelectedText(); ok {
					t.Error("a collapsed selection reported selected text")
				}
			},
			want:       "abcdef",
			wantCaret:  6,
			wantMarked: -1,
			wantOps:    []string{`replace(0,0,"")`, `replace(6,6,"")`},
		},
		{
			name: "selected text is read, and a range selection is refused not faked",
			text: "abcdef",
			sel:  &[2]int{2, 5},
			calls: func(t *testing.T, m *Ime) {
				if got, ok := m.SelectedText(); !ok || got != "cde" {
					t.Errorf("SelectedText()=%q ok=%v, want \"cde\" true", got, ok)
				}
				if m.SetSelection(1, 4) {
					t.Error("SetSelection claimed to select a range TextInputClient cannot select")
				}
			},
			// The refusal is the point: replacing [1,4] with the empty string
			// is the only edit the contract offers for it, and it would
			// delete the text the input method asked to select.
			want:       "abcdef",
			wantCaret:  5,
			wantMarked: -1,
			wantOps:    []string{},
		},
		{
			name:  "commit puts the caret where newCursorPosition asks",
			text:  "ab",
			caret: 2,
			calls: func(t *testing.T, m *Ime) {
				// 0 means "at the start of the committed text", not "after it".
				if !m.CommitText("XY", 0) {
					t.Error("CommitText reported no client")
				}
			},
			want:       "abXY",
			wantCaret:  2,
			wantMarked: -1,
			wantOps:    []string{`replace(2,2,"XY")`, `replace(2,2,"")`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &recordingClient{text: tt.text}
			c.sel = platform.TextSelection{Range: platform.TextRange{Start: tt.caret, End: tt.caret}}
			if tt.sel != nil {
				c.sel.Range = platform.TextRange{Start: tt.sel[0], End: tt.sel[1]}
			}
			m := &Ime{}
			m.SetClient(c)
			c.ops = nil

			tt.calls(t, m)

			if c.text != tt.want {
				t.Errorf("text = %q, want %q", c.text, tt.want)
			}
			if got := c.sel.Caret(); got != tt.wantCaret {
				t.Errorf("caret = %d, want %d", got, tt.wantCaret)
			}
			if got := c.markedUnits(); got != tt.wantMarked {
				t.Errorf("marked = %d units, want %d", got, tt.wantMarked)
			}
			if tt.wantOps != nil && !slices.Equal(c.ops, tt.wantOps) {
				t.Errorf("client calls:\n got %v\nwant %v", c.ops, tt.wantOps)
			}
		})
	}
}

// TestTextInputUnmarkThenCommit is the case platform.InputComposition exists
// for, on its own so that a failure here cannot be read as a failure of the
// ordinary commit above.
//
// Google's Japanese and Chinese input methods end the preedit before they
// deliver its result: finishComposingText, then commitText. The preedit is no
// longer marked when the commit arrives, so a commit that replaced the
// selection would leave the preedit in the document and insert the result
// after it. End remembers the range it unmarked; Replace substitutes it for
// the commit's nil range; the preedit is what gets replaced.
func TestTextInputUnmarkThenCommit(t *testing.T) {
	c := &recordingClient{text: ""}
	c.sel = platform.TextSelection{Range: platform.TextRange{Start: 0, End: 0}}
	m := &Ime{}
	m.SetClient(c)

	m.SetComposingText("niha", 1)
	if !m.FinishComposingText() {
		t.Fatal("FinishComposingText reported no client")
	}
	// The preedit's text stays — finishComposingText keeps it — but it is no
	// longer marked.
	if c.text != "niha" {
		t.Fatalf("after finishComposingText text = %q, want %q", c.text, "niha")
	}
	if c.markedUnits() != -1 {
		t.Fatalf("after finishComposingText marked = %d units, want none", c.markedUnits())
	}
	m.CommitText("你哈", 1)

	if c.text != "你哈" {
		t.Errorf("text = %q, want %q (the preedit was left behind)", c.text, "你哈")
	}
	if got := c.sel.Caret(); got != 2 {
		t.Errorf("caret = %d, want 2", got)
	}
	want := []string{`mark(0,0,"niha",4)`, "unmark", `replace(0,4,"你哈")`}
	if !slices.Equal(c.ops, want) {
		t.Errorf("client calls:\n got %v\nwant %v", c.ops, want)
	}
}

// TestTextInputFocus pins who owns the preedit when the keyboard moves: the
// old field keeps the text its preedit had become, stops calling it
// composing, and the next commit goes to the new field. It also pins the
// keyboard sink's contract, which is what raises the soft keyboard.
// caretAtEnd puts a field's caret where someone who just typed the text would
// have it, so a preedit lands after the text instead of before it.
func caretAtEnd(c *recordingClient) {
	n := platform.UTF16Len(c.text)
	c.sel = platform.TextSelection{Range: platform.TextRange{Start: n, End: n}}
}

func TestTextInputFocus(t *testing.T) {
	a := &recordingClient{text: "one"}
	b := &recordingClient{text: "two"}
	caretAtEnd(a)
	caretAtEnd(b)

	m := &Ime{}
	var active []bool
	m.OnActive = func(on bool) { active = append(active, on) }

	// Nothing focused: no edit, no read, and the keyboard is never asked for.
	if m.SetComposingText("x", 1) {
		t.Error("an edit was applied with nothing focused")
	}
	if _, ok := m.TextBeforeCursor(1); ok {
		t.Error("a caret was read with nothing focused")
	}
	if m.FinishComposingText() {
		t.Error("FinishComposingText reported a client with nothing focused")
	}
	if len(active) != 0 {
		t.Fatalf("keyboard states = %v before any text input", active)
	}

	m.SetTextInput(platform.TextInputState{Active: true, Client: a})
	m.SetComposingText("ni", 1)
	if a.markedUnits() != 2 {
		t.Fatalf("a marked = %d units, want 2", a.markedUnits())
	}

	// The keyboard moves to another field mid-composition.
	m.SetTextInput(platform.TextInputState{Active: true, Client: b})
	if a.markedUnits() != -1 {
		t.Error("the field that lost focus still reports marked text")
	}
	if a.text != "oneni" {
		t.Errorf("the field that lost focus lost its preedit's text: %q, want %q", a.text, "oneni")
	}
	m.CommitText("好", 1)
	if b.text != "two好" {
		t.Errorf("b.text = %q, want %q (the commit went to the wrong field)", b.text, "two好")
	}
	if a.text != "oneni" {
		t.Errorf("a.text = %q, want %q", a.text, "oneni")
	}

	// An editable element with no client is not editable by this backend, so
	// it must not raise a keyboard that cannot type.
	m.SetTextInput(platform.TextInputState{Active: true})
	if m.Client() != nil {
		t.Error("a client was adopted from a state that had none")
	}

	// Losing the keyboard drops the client and everything an edit would do.
	m.SetTextInput(platform.TextInputState{Active: false})
	if m.Client() != nil {
		t.Error("the client survived losing the keyboard")
	}
	if m.CommitText("x", 1) {
		t.Error("an edit was applied after the keyboard was lost")
	}

	want := []bool{true, true, false, false}
	if !slices.Equal(active, want) {
		t.Errorf("keyboard states = %v, want %v", active, want)
	}
}

// TestOptionsReachTheHost covers the path that lets an element say what its
// Return key is: Ime.SetTextInput reports TextInputState.Options through
// OnOptions, and only when it changes.
//
// What it protects: without this report the Android side kept the traits
// MygoView hardcodes — IME_ACTION_NONE among them — so a Return key was never
// an action, the input method consumed Enter itself, and a terminal could not
// run a command. The report is what carries ReturnSend to Java.
func TestOptionsReachTheHost(t *testing.T) {
	m := &Ime{}
	var got []platform.TextInputOptions
	m.OnOptions = func(o platform.TextInputOptions) { got = append(got, o) }
	c := &recordingClient{}

	send := platform.TextInputOptions{Return: "send"}
	m.SetTextInput(platform.TextInputState{Active: true, Client: c, Options: send})
	m.SetTextInput(platform.TextInputState{Active: true, Client: c, Options: send})
	m.SetTextInput(platform.TextInputState{Active: true, Client: c, Options: platform.TextInputOptions{Return: "done"}})

	if len(got) != 2 {
		t.Fatalf("OnOptions ran %d times, want 2: an unchanged state must not report again", len(got))
	}
	if got[0].Return != "send" {
		t.Errorf("first report Return = %q, want \"send\"", got[0].Return)
	}
	if got[1].Return != "done" {
		t.Errorf("second report Return = %q, want \"done\"", got[1].Return)
	}
	if m.Options().Return != "done" {
		t.Errorf("Options() = %q, want the last reported \"done\"", m.Options().Return)
	}
}

// TestOptionsStoredWithoutHost keeps Options readable when nothing listens:
// the Java side asks for it while building an InputConnection, which can
// happen before package main has installed OnOptions.
func TestOptionsStoredWithoutHost(t *testing.T) {
	m := &Ime{}
	m.SetTextInput(platform.TextInputState{Active: true, Client: &recordingClient{}, Options: platform.TextInputOptions{Return: "send"}})
	if got := m.Options().Return; got != "send" {
		t.Errorf("Options() = %q, want \"send\"", got)
	}
}
