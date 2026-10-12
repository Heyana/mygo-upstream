//go:build android && cgo

// The Android text-input entry points: what org.mygo.MygoIme calls for every
// InputConnection request, and the one Java method Go calls back for the
// keyboard.
//
// # Direction
//
// An input method asks Java, Java asks Go, Go asks the focused element's
// platform.TextInputClient — and every answer travels back the same way.
//
//	IME (system)
//	  ↓ setComposingText / commitText / finishComposingText /
//	    deleteSurroundingText / setSelection / getTextBeforeCursor / …
//	MygoImeConnection (Java, the InputConnection)
//	  ↓ static calls
//	Java_org_mygo_MygoIme_native* (this file)
//	  ↓ theBackend.Post + wait
//	Ime (Go, internal/android/backend/textinput.go)   ← the translation
//	  ↓ TextInputClient
//	the focused element's text client (package ui)
//
// The reverse direction exists in one place. When an editable element takes
// or loses the keyboard, internal/android/backend/textinput.go reports it
// through Ime.OnActive, and this file turns that into
// MygoIme.setSoftKeyboard(View, boolean) on Java's main thread — the
// keyboard cannot be raised without Java, and Java cannot know when a mygo
// element became editable without Go.
//
// # Why Java waits for the pump
//
// An InputConnection method is synchronous: an input method reads
// getTextBeforeCursor immediately after commitText and expects the committed
// text to be there. The client's callbacks are documented to run on the
// backend's main (pump) goroutine, and the Java thread is not it — the Activity
// owns the process' main thread and no Go code runs there (see app.go). So
// each entry point hands the work to the pump and waits for the answer. That
// is safe precisely because the pump never waits on Java: the one call it
// makes into Java (setSoftKeyboard) only posts and returns.
//
// # Why the strings are UTF-16
//
// Android counts Java chars, which are UTF-16 code units, and so does
// platform.TextRange. Reading a jstring with GetStringUTFChars or building one
// with NewStringUTF would go through modified UTF-8 — which encodes a
// surrogate pair as six bytes and would shift every offset after an emoji.
// The characters are converted here instead, so an offset Java reported and an
// offset Go computes refer to the same unit.
package main

/*
#include <jni.h>
#include <stdint.h>
#include <stdlib.h>

// ── strings: Java's UTF-16 chars, never modified UTF-8 ──────────────────

static jsize mygo_ime_strlen(JNIEnv *env, jstring s) {
	return (*env)->GetStringLength(env, s);
}

static const jchar *mygo_ime_chars(JNIEnv *env, jstring s) {
	// isCopy stays NULL: the runtime may hand back a direct pointer into the
	// Java string, and ReleaseStringChars below is the other half of either
	// case.
	return (*env)->GetStringChars(env, s, NULL);
}

static void mygo_ime_release(JNIEnv *env, jstring s, const jchar *chars) {
	(*env)->ReleaseStringChars(env, s, chars);
}

static jstring mygo_ime_new(JNIEnv *env, const jchar *chars, jsize n) {
	// A NULL pointer with length 0 is legal (the string is empty); the Go
	// side passes one rather than a Go pointer into a zero-length slice.
	return (*env)->NewString(env, chars, n);
}

// The null checks and the null string go through C because Go cannot write
// them: jni.h's C branch types these as "void*" typedefs, which cgo turns
// into defined Go types that a plain nil does not fit — and spelling them
// with unsafe.Pointer conversions is what go vet calls a possible misuse.
static int mygo_ime_is_null_string(jstring s) { return s == NULL; }
static int mygo_ime_is_null_obj(jobject o) { return o == NULL; }
static jstring mygo_ime_null_string(void) { return NULL; }

// ── the handles the Go→Java direction needs ─────────────────────────────
//
// They are kept as uintptr_t so no JNI type has to live in a Go variable:
// a JavaVM* and a global jobject are process-wide handles that the JVM
// itself keeps alive, and this is the usual way to hold one in Go.

static uintptr_t mygo_ime_vm(JNIEnv *env) {
	JavaVM *vm = NULL;
	if (env == NULL) {
		return 0;
	}
	if ((*env)->GetJavaVM(env, &vm) != JNI_OK) {
		return 0;
	}
	return (uintptr_t)vm;
}

static uintptr_t mygo_ime_global(JNIEnv *env, jobject obj) {
	if (env == NULL || obj == NULL) {
		return 0;
	}
	return (uintptr_t)(*env)->NewGlobalRef(env, obj);
}

static void mygo_ime_drop_global(JNIEnv *env, uintptr_t ref) {
	if (env != NULL && ref != 0) {
		(*env)->DeleteGlobalRef(env, (jobject)ref);
	}
}

// mygo_ime_keyboard_method looks up the one method Go calls: it is declared
// on org.mygo.MygoIme itself, and the object it is called on is the same
// object that bound the view, so GetObjectClass finds the class without a
// FindClass (which an attached native thread cannot be relied on to do with
// the right class loader).
static uintptr_t mygo_ime_keyboard_method(JNIEnv *env, uintptr_t obj) {
	if (env == NULL || obj == 0) {
		return 0;
	}
	jclass cls = (*env)->GetObjectClass(env, (jobject)obj);
	if (cls == NULL) {
		return 0;
	}
	jmethodID mid = (*env)->GetMethodID(env, cls, "setSoftKeyboard", "(Landroid/view/View;Z)V");
	(*env)->DeleteLocalRef(env, cls);
	if ((*env)->ExceptionCheck(env)) {
		(*env)->ExceptionClear(env);
		return 0;
	}
	return (uintptr_t)mid;
}

// mygo_ime_keyboard raises or lowers the soft keyboard from whatever thread
// Go is on.
//
// The thread is attached once and never detached. The caller is the backend's
// pump, which locked itself to an OS thread for the life of the process
// (app.go's pump does runtime.LockOSThread), so it is the same thread every
// time and the attachment stays valid. Detaching after the call would be
// worse than useless: a Go goroutine can be moved to another OS thread
// between two instructions, and DetachCurrentThread on a thread it no longer
// runs on is a crash.
static int mygo_ime_keyboard(uintptr_t vm, uintptr_t obj, uintptr_t mid, uintptr_t view, int shown) {
	if (vm == 0 || obj == 0 || mid == 0 || view == 0) {
		return 0;
	}
	JNIEnv *env = NULL;
	jint r = (*(JavaVM *)vm)->GetEnv((JavaVM *)vm, (void **)&env, JNI_VERSION_1_6);
	if (r == JNI_EDETACHED) {
		if ((*(JavaVM *)vm)->AttachCurrentThread((JavaVM *)vm, (JNIEnv **)&env, NULL) != JNI_OK) {
			return 0;
		}
	} else if (r != JNI_OK) {
		return 0;
	}
	(*env)->CallVoidMethod(env, (jobject)obj, (jmethodID)mid, (jobject)view, (jboolean)shown);
	// A pending exception left on this thread would make every later JNI call
	// on it behave strangely, and this thread is not Java's to unwind.
	if ((*env)->ExceptionCheck(env)) {
		(*env)->ExceptionDescribe(env);
		(*env)->ExceptionClear(env);
		return 0;
	}
	return 1;
}
*/
import "C"

import (
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/egoist/mygo/internal/android/backend"
)

func init() {
	// The Go→Java half of the keyboard. Package main is the only place that
	// may talk to Java, and the state machine is the only place that knows an
	// editable element took or lost the keyboard, so this is where the two
	// meet.
	backend.TheIme().OnActive = setSoftKeyboard
}

// imeWait bounds how long a Java thread waits for the pump. The pump drops
// queued work once the backend quits (Backend.Post's quit case), which is the
// one way a request can never be answered; a Java thread inside
// InputConnection must answer anyway, and blocking it forever would be an
// ANR. Answering "no text" is the honest answer during teardown.
const imeWait = 2 * time.Second

// imeCall runs fn on the pump goroutine and waits for it, reporting whether
// it ran. Nothing inside fn may wait on Java.
func imeCall(fn func()) bool {
	done := make(chan struct{})
	ran := false
	theBackend.Post(func() {
		ran = true
		defer close(done)
		fn()
	})
	select {
	case <-done:
		return ran
	case <-time.After(imeWait):
		androidLog("ime: the backend did not answer a text-input request within %v (quitting?)", imeWait)
		return false
	}
}

// goString copies a Java string into Go as UTF-16 code units, so that an
// offset Java reported and a platform.TextRange refer to the same unit.
//
// Called on the Java thread: the pointer GetStringChars returns is only valid
// there, and only until the matching Release.
func goString(env *C.JNIEnv, s C.jstring) string {
	if env == nil || C.mygo_ime_is_null_string(s) != 0 {
		return ""
	}
	n := C.mygo_ime_strlen(env, s)
	if n <= 0 {
		return ""
	}
	chars := C.mygo_ime_chars(env, s)
	if chars == nil {
		return ""
	}
	units := unsafe.Slice((*uint16)(unsafe.Pointer(chars)), int(n))
	out := string(utf16.Decode(units))
	C.mygo_ime_release(env, s, chars)
	return out
}

// javaString builds a Java string from Go's UTF-16, the same way round.
//
// NewString copies, so the pointer into the Go slice does not outlive the
// call (the cgo pointer rules are satisfied: the slice holds no Go pointers).
func javaString(env *C.JNIEnv, s string) C.jstring {
	if env == nil {
		return noString()
	}
	units := utf16.Encode([]rune(s))
	if len(units) == 0 {
		return C.mygo_ime_new(env, nil, 0)
	}
	return C.mygo_ime_new(env, (*C.jchar)(unsafe.Pointer(&units[0])), C.jsize(len(units)))
}

// jstringOrNil is a Java string for s, or Java's null when there is nothing
// to answer with. Android's getSelectedText means null by that, and so does
// getTextBeforeCursor when nothing is focused.
func jstringOrNil(env *C.JNIEnv, s string, ok bool) C.jstring {
	if !ok || env == nil {
		return noString()
	}
	return javaString(env, s)
}

// noString is Java's null string. It is built in C because C.jstring is a
// defined type over unsafe.Pointer (jni.h's C branch is "typedef void*
// jobject"), which a plain nil does not fit.
func noString() C.jstring { return C.mygo_ime_null_string() }

// jboolean spells Go's bool as Java's.
func jboolean(b bool) C.jboolean {
	if b {
		return 1
	}
	return 0
}

// ── the handles, written only by bindView (Java thread) and read by
// setSoftKeyboard (the pump). The mutex covers the window where Go is on the
// pump and Java is on the Activity's thread.
var (
	imeMu        sync.Mutex
	javaVM       uintptr
	imeObject    uintptr
	imeKeyboard  uintptr
	imeKeyboardV uintptr
)

// Java_org_mygo_MygoIme_bindView is MygoIme.bindView: it tells Go which View
// is the surface an input method types into, and the MygoIme object whose
// setSoftKeyboard Go calls back.
//
// MygoView calls it once, when it is created, rather than when a connection
// is first requested: Go has to be able to raise the keyboard for an element
// that just became editable, and at that moment no InputConnection exists yet
// — the system makes one only after it is asked to show the keyboard.
//
// Runs on Java's main thread, and only touches the three handles.
//
//export Java_org_mygo_MygoIme_bindView
func Java_org_mygo_MygoIme_bindView(env *C.JNIEnv, thiz C.jobject, view C.jobject) {
	if env == nil || C.mygo_ime_is_null_obj(view) != 0 {
		return
	}
	imeMu.Lock()
	defer imeMu.Unlock()
	if javaVM == 0 {
		javaVM = uintptr(C.mygo_ime_vm(env))
	}
	if imeObject == 0 && C.mygo_ime_is_null_obj(thiz) == 0 {
		if ref := uintptr(C.mygo_ime_global(env, thiz)); ref != 0 {
			imeObject = ref
			imeKeyboard = uintptr(C.mygo_ime_keyboard_method(env, C.uintptr_t(ref)))
		}
	}
	if ref := uintptr(C.mygo_ime_global(env, view)); ref != 0 {
		if imeKeyboardV != 0 {
			C.mygo_ime_drop_global(env, C.uintptr_t(imeKeyboardV))
		}
		imeKeyboardV = ref
	}
	androidLog("ime: view bound vm=%v object=%v method=%v view=%#x", javaVM != 0, imeObject != 0, imeKeyboard != 0, imeKeyboardV)
}

// setSoftKeyboard is the Go→Java direction: it runs on the pump when an
// editable element takes or loses the keyboard.
//
// It only calls the Java method, which posts to the main thread and returns.
// Doing the InputMethodManager work here instead would block the pump on
// Java, while Java may be blocked inside an InputConnection method waiting
// for the pump — the two would wait for each other.
func setSoftKeyboard(active bool) {
	imeMu.Lock()
	vm, obj, mid, view := javaVM, imeObject, imeKeyboard, imeKeyboardV
	imeMu.Unlock()
	if vm == 0 || obj == 0 || mid == 0 || view == 0 {
		androidLog("ime: keyboard=%v but no view is bound (MygoView.bindView never ran?)", active)
		return
	}
	shown := 0
	if active {
		shown = 1
	}
	if C.mygo_ime_keyboard(C.uintptr_t(vm), C.uintptr_t(obj), C.uintptr_t(mid), C.uintptr_t(view), C.int(shown)) == 0 {
		androidLog("ime: setSoftKeyboard(%v) failed", active)
		return
	}
	androidLog("ime: keyboard=%v", active)
}

// The editing entry points. Each one has Android's InputConnection signature,
// and each answers with what the state machine said: false tells an input
// method the edit did not happen, which is true when no client is focused.
//
// The Java thread names are the //export symbols Java binds to
// org.mygo.MygoIme's external methods, so they are spelled out by the Kotlin
// side and must not change: Java_org_mygo_MygoIme_<method>.

// Java_org_mygo_MygoIme_nativeSetComposingText is setComposingText.
//
//export Java_org_mygo_MygoIme_nativeSetComposingText
func Java_org_mygo_MygoIme_nativeSetComposingText(env *C.JNIEnv, thiz C.jobject, text C.jstring, newCursorPosition C.jint) C.jboolean {
	s := goString(env, text)
	var ok bool
	ran := imeCall(func() { ok = backend.TheIme().SetComposingText(s, int(newCursorPosition)) })
	return jboolean(ran && ok)
}

// Java_org_mygo_MygoIme_nativeCommitText is commitText.
//
//export Java_org_mygo_MygoIme_nativeCommitText
func Java_org_mygo_MygoIme_nativeCommitText(env *C.JNIEnv, thiz C.jobject, text C.jstring, newCursorPosition C.jint) C.jboolean {
	s := goString(env, text)
	var ok bool
	ran := imeCall(func() { ok = backend.TheIme().CommitText(s, int(newCursorPosition)) })
	return jboolean(ran && ok)
}

// Java_org_mygo_MygoIme_nativeFinishComposingText is finishComposingText.
//
//export Java_org_mygo_MygoIme_nativeFinishComposingText
func Java_org_mygo_MygoIme_nativeFinishComposingText(env *C.JNIEnv, thiz C.jobject) C.jboolean {
	var ok bool
	ran := imeCall(func() { ok = backend.TheIme().FinishComposingText() })
	return jboolean(ran && ok)
}

// Java_org_mygo_MygoIme_nativeDeleteSurroundingText is
// deleteSurroundingText.
//
//export Java_org_mygo_MygoIme_nativeDeleteSurroundingText
func Java_org_mygo_MygoIme_nativeDeleteSurroundingText(env *C.JNIEnv, thiz C.jobject, before C.jint, after C.jint) C.jboolean {
	var ok bool
	ran := imeCall(func() { ok = backend.TheIme().DeleteSurroundingText(int(before), int(after)) })
	return jboolean(ran && ok)
}

// Java_org_mygo_MygoIme_nativeSetSelection is setSelection. A range that
// selects text answers false: see Ime.SetSelection for why it is not faked.
//
//export Java_org_mygo_MygoIme_nativeSetSelection
func Java_org_mygo_MygoIme_nativeSetSelection(env *C.JNIEnv, thiz C.jobject, start C.jint, end C.jint) C.jboolean {
	var ok bool
	ran := imeCall(func() { ok = backend.TheIme().SetSelection(int(start), int(end)) })
	return jboolean(ran && ok)
}

// Java_org_mygo_MygoIme_nativeTextBeforeCursor is getTextBeforeCursor. A nil
// answer is Android's "there is no text", which is what an input method gets
// when nothing is focused — and what it must get rather than a hang.
//
//export Java_org_mygo_MygoIme_nativeTextBeforeCursor
func Java_org_mygo_MygoIme_nativeTextBeforeCursor(env *C.JNIEnv, thiz C.jobject, n C.jint) C.jstring {
	var text string
	var ok bool
	ran := imeCall(func() { text, ok = backend.TheIme().TextBeforeCursor(int(n)) })
	return jstringOrNil(env, text, ran && ok)
}

// Java_org_mygo_MygoIme_nativeTextAfterCursor is getTextAfterCursor.
//
//export Java_org_mygo_MygoIme_nativeTextAfterCursor
func Java_org_mygo_MygoIme_nativeTextAfterCursor(env *C.JNIEnv, thiz C.jobject, n C.jint) C.jstring {
	var text string
	var ok bool
	ran := imeCall(func() { text, ok = backend.TheIme().TextAfterCursor(int(n)) })
	return jstringOrNil(env, text, ran && ok)
}

// Java_org_mygo_MygoIme_nativeSelectedText is getSelectedText, and nil is
// Android's answer for a collapsed selection — not an error.
//
//export Java_org_mygo_MygoIme_nativeSelectedText
func Java_org_mygo_MygoIme_nativeSelectedText(env *C.JNIEnv, thiz C.jobject) C.jstring {
	var text string
	var ok bool
	ran := imeCall(func() { text, ok = backend.TheIme().SelectedText() })
	return jstringOrNil(env, text, ran && ok)
}

// Java_org_mygo_MygoIme_nativeSelectionStart is the selection's start for
// EditorInfo.initialSelStart, or -1 when nothing is focused.
//
//export Java_org_mygo_MygoIme_nativeSelectionStart
func Java_org_mygo_MygoIme_nativeSelectionStart(env *C.JNIEnv, thiz C.jobject) C.jint {
	var start int
	var ok bool
	imeCall(func() { start, _, ok = backend.TheIme().Selection() })
	if !ok {
		return -1
	}
	return C.jint(start)
}

// Java_org_mygo_MygoIme_nativeSelectionEnd is the selection's end, or -1.
//
//export Java_org_mygo_MygoIme_nativeSelectionEnd
func Java_org_mygo_MygoIme_nativeSelectionEnd(env *C.JNIEnv, thiz C.jobject) C.jint {
	var end int
	var ok bool
	imeCall(func() { _, end, ok = backend.TheIme().Selection() })
	if !ok {
		return -1
	}
	return C.jint(end)
}
