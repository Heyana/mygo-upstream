//go:build !js

// testDocs, which these use, is document_test.go's and that file builds
// everywhere but js — the browser has no document files or windows to open,
// so this follows it.

package mygo

import (
	"io"
	"testing"
	"time"
)

// TestDocumentEqualReadsDocument is the shape DocumentsOptions.Equal takes
// when an app compares against what the document holds: the callback reads
// the document. It deadlocked once — Set and markSaved called Equal holding
// docBase.mu, and Content takes the same mutex, so the frame that edited (or
// saved) the document stopped on itself. ImageCache.ask is the same mistake
// (0487c37b); this one was on every platform, not just the desktop.
//
// The watchdog is the test: a deadlock does not fail an assertion, it stops
// the goroutine from ever returning, so a timeout is the only way it is seen.
func TestDocumentEqualReadsDocument(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(t *testing.T, doc *Document[string])
	}{
		{"Set", func(_ *testing.T, doc *Document[string]) { doc.Set("edited") }},
		{"Changed", func(_ *testing.T, doc *Document[string]) { doc.Changed() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc *Document[string]
			ds := testDocs(t, DocumentsOptions[string]{
				Equal: func(a, b string) bool { return a == doc.Content() },
			})
			doc = ds.New()

			done := make(chan struct{})
			go func() {
				defer close(done)
				tc.edit(t, doc)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("Document.%s deadlocked: Equal ran while docBase.mu was held and re-entered it", tc.name)
			}
		})
	}
}

// TestDocumentEqualStillEdits is the behaviour the fix must keep: an app whose
// Equal says the content is what the file has leaves the document unedited,
// and an Equal that reads the document is a legal way to say it. The saved
// content of a new document is empty, which is what SetEdited(false) records
// when the app's own store holds nothing yet.
func TestDocumentEqualStillEdits(t *testing.T) {
	var doc *Document[string]
	ds := testDocs(t, DocumentsOptions[string]{
		// Reading the document here is what used to deadlock; comparing
		// a to b is the behaviour the option is for.
		Equal: func(a, b string) bool { _ = doc.Content(); return a == b },
	})
	doc = ds.New()
	doc.SetEdited(false) // the file's content, as the app read it

	doc.Set("something")
	if !doc.IsEdited() {
		t.Error("content different from the file's did not edit the document")
	}
	// Undoing back to what the file has is not an edit, which is the whole
	// reason Equal exists.
	doc.Set("")
	if doc.IsEdited() {
		t.Error("content equal to the file's left the document edited")
	}
}

// TestDocumentEqualUncomparableContent takes the type Equal exists for out of
// the happy path: content is a document's bytes, which cannot be compared with
// ==, so the app passes Equal to say when two are the same. Settling what
// Equal said by comparing the values again panicked on that type, so the whole
// reason to pass Equal was the one case it broke.
func TestDocumentEqualUncomparableContent(t *testing.T) {
	type bytes []byte
	read := func(r io.Reader) (bytes, error) { b, err := io.ReadAll(r); return bytes(b), err }
	ds := NewDocuments(DocumentsOptions[bytes]{
		Types:      []DocumentType{{Name: "Bytes " + t.Name(), Extensions: []string{"bin"}}},
		Equal:      func(a, b bytes) bool { return string(a) == string(b) },
		Read:       read,
		NoUntitled: true,
		Write: func(w io.Writer, b bytes) error {
			_, err := w.Write(b)
			return err
		},
		Window: func(doc *Document[bytes]) *Window { return NewWindow(WindowOptions{}) },
	})
	doc := ds.New()
	doc.SetEdited(false) // the file's content, as the app read it

	doc.Set(bytes{1, 2})
	if !doc.IsEdited() {
		t.Error("content different from the file's did not edit the document")
	}
	doc.Set(bytes{})
	if doc.IsEdited() {
		t.Error("content equal to the file's left the document edited")
	}
}

// TestSettleSavedComparesUncomparable pins the comparison itself: settling
// what Equal said used to compare the two values again, and a content type
// that cannot be compared with == — the only reason to pass Equal — panicked
// there. TestDocumentEqualUncomparableContent walks the same ground through
// the public API, but only this one reaches the frame, so it is what fails
// when the comparison regresses.
func TestSettleSavedComparesUncomparable(t *testing.T) {
	type bytes []byte
	var doc *Document[bytes]
	ds := NewDocuments(DocumentsOptions[bytes]{
		Types:      []DocumentType{{Name: "Probe", Extensions: []string{"bin"}}},
		Equal:      func(a, b bytes) bool { _ = doc; return string(a) == string(b) },
		NoUntitled: true,
		Read: func(r io.Reader) (bytes, error) {
			b, err := io.ReadAll(r)
			return bytes(b), err
		},
		Write:  func(w io.Writer, b bytes) error { _, err := w.Write(b); return err },
		Window: func(*Document[bytes]) *Window { return NewWindow(WindowOptions{}) },
	})
	doc = ds.New()
	v := bytes{1, 2}
	doc.Set(v)
	doc.docBase.mu.Lock()
	doc.saved, doc.savedKnown = v, true
	doc.docBase.mu.Unlock()
	doc.settleSaved(true, any(v), any(v))
}
