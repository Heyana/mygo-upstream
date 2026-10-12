//go:build android && cgo

package main

/*
#include <jni.h>

// The arrays a MotionEvent's pointers arrive in are read out with a JNI
// call that copies them (Get*ArrayRegion) rather than one that pins them
// (Get*ArrayElements): the decoded values travel to the backend's pump
// goroutine, and a pinned Java array must not stay pinned while another
// thread waits. The helpers exist because cgo cannot call a function
// pointer field of JNIEnv directly, and each takes exactly the array type
// it reads — a jintArray and a jfloatArray are distinct types to cgo even
// though both are pointers.

static jint mygo_input_int_array_len(JNIEnv *env, jintArray a) {
	return (*env)->GetArrayLength(env, a);
}

static jint mygo_input_float_array_len(JNIEnv *env, jfloatArray a) {
	return (*env)->GetArrayLength(env, a);
}

static void mygo_input_int_array_get(JNIEnv *env, jintArray a, jint start, jint len, jint *buf) {
	(*env)->GetIntArrayRegion(env, a, start, len, buf);
}

static void mygo_input_float_array_get(JNIEnv *env, jfloatArray a, jint start, jint len, jfloat *buf) {
	(*env)->GetFloatArrayRegion(env, a, start, len, buf);
}
*/
import "C"

import (
	"github.com/egoist/mygo/internal/android/backend"
)

// This file is the JNI half of Android input: org.mygo.MygoInput's native
// methods, whose Java names the Kotlin side spells out
// (Java_org_mygo_MygoInput_<method>), decoded into the plain Go values
// internal/android/backend/input.go maps and tests.
//
// It does no mapping itself, for the two reasons jni.go states about the
// whole layer: it runs on whatever thread Java called on, so it must not
// touch backend state (every such call goes through theBackend.Post), and
// what it can do is exactly what a desktop test cannot reach — read the
// event's arrays. Everything the mapping decides is in package backend,
// where CGO_ENABLED=0 go test runs it.

// Java_org_mygo_MygoInput_nativeOnDisplayInfo is nativeOnDisplayInfo: the
// display's density in dots per inch, from
// resources.displayMetrics.densityDpi. One call per surface change turns
// every later event's view pixels into DIPs.
//
//export Java_org_mygo_MygoInput_nativeOnDisplayInfo
func Java_org_mygo_MygoInput_nativeOnDisplayInfo(env *C.JNIEnv, self C.jobject, dpi C.jint) {
	d := int(dpi)
	theBackend.Post(func() { backend.DisplayInfo(d) })
}

// Java_org_mygo_MygoInput_nativeOnTouchBatch is nativeOnTouchBatch: one
// MotionEvent, whole. action is event.getActionMasked(), the ids and the
// coordinates are the event's pointers (event.getPointerId, getX, getY) in
// the view's own pixels, actionIndex is event.getActionIndex() and
// buttonState, metaState and eventTimeMs are the event's own fields. A
// scroll's axes come in scrollX and scrollY.
//
// Passing the whole event in one call, rather than one call per pointer, is
// what lets the mapping see a two-finger gesture as Android reported it:
// the pointer set of a single event is the thing that cannot be
// reconstructed from per-pointer calls that arrive apart.
//
//export Java_org_mygo_MygoInput_nativeOnTouchBatch
func Java_org_mygo_MygoInput_nativeOnTouchBatch(env *C.JNIEnv, self C.jobject,
	action C.jint, ids C.jintArray, xs, ys C.jfloatArray,
	actionIndex, buttonState, metaState C.jint,
	scrollX, scrollY C.jfloat, eventTimeMs C.jlong) {

	// cgo types a jintArray as a uintptr, so "absent" is 0 rather than nil.
	// A call with any of the three missing has no pointers to read at all,
	// and one with arrays of different lengths is clamped to the shortest:
	// a Java side that sent ragged arrays must not be read past the end of
	// one of them.
	n := 0
	if ids != 0 && xs != 0 && ys != 0 {
		n = int(C.mygo_input_int_array_len(env, ids))
		if nx := int(C.mygo_input_float_array_len(env, xs)); nx < n {
			n = nx
		}
		if ny := int(C.mygo_input_float_array_len(env, ys)); ny < n {
			n = ny
		}
	}

	in := backend.TouchInput{
		Action:      int(action),
		ActionIndex: int(actionIndex),
		ButtonState: int(buttonState),
		MetaState:   int(metaState),
		ScrollX:     float32(scrollX),
		ScrollY:     float32(scrollY),
		EventTimeMs: int64(eventTimeMs),
	}
	if n > 0 {
		// Go memory, not Java memory: these buffers are what the pump
		// goroutine reads, and the JNI calls have already copied into them
		// by the time Post is reached.
		idBuf := make([]C.jint, n)
		xBuf := make([]C.jfloat, n)
		yBuf := make([]C.jfloat, n)
		C.mygo_input_int_array_get(env, ids, 0, C.jint(n), &idBuf[0])
		C.mygo_input_float_array_get(env, xs, 0, C.jint(n), &xBuf[0])
		C.mygo_input_float_array_get(env, ys, 0, C.jint(n), &yBuf[0])
		pointers := make([]backend.Pointer, n)
		for i := 0; i < n; i++ {
			pointers[i] = backend.Pointer{ID: int(idBuf[i]), X: float32(xBuf[i]), Y: float32(yBuf[i])}
		}
		in.Pointers = pointers
	}

	// A move arrives tens of times a second; logging every one of them
	// would bury the logcat lines that matter. Everything else is logged.
	if int(action) != androidActionMove {
		androidLog("onTouch action=%d pointers=%d index=%d button=%d t=%d",
			int(action), n, int(actionIndex), int(buttonState), int64(eventTimeMs))
	}
	theBackend.Post(func() { backend.TouchBatch(in) })
}

// Java_org_mygo_MygoInput_nativeOnKeyEvent is nativeOnKeyEvent: one
// KeyEvent. keyCode, action and metaState are KeyEvent's own fields;
// unicodeChar is keyEvent.getUnicodeChar(), the character the key types with
// the layout and the shift applied (0 when it types nothing, which is how
// the Go side tells text from a bare key without a layout table); repeatCount
// above 0 marks an auto-repeat.
//
//export Java_org_mygo_MygoInput_nativeOnKeyEvent
func Java_org_mygo_MygoInput_nativeOnKeyEvent(env *C.JNIEnv, self C.jobject,
	keyCode, action, metaState, unicodeChar, repeatCount C.jint, eventTimeMs C.jlong) {

	in := backend.KeyEventInput{
		KeyCode:     int(keyCode),
		Action:      int(action),
		MetaState:   int(metaState),
		UnicodeChar: int(unicodeChar),
		RepeatCount: int(repeatCount),
		EventTimeMs: int64(eventTimeMs),
	}
	theBackend.Post(func() {
		if outcome := backend.KeyBatch(in); outcome == backend.KeyUnmapped {
			// Recorded, not dropped silently: Android has keys
			// platform.Key does not name, and this is the only place that
			// can say which one arrived.
			androidLog("onKey unmapped %s (code %d, action %d)",
				backend.KeyCodeName(in.KeyCode), in.KeyCode, in.Action)
		}
	})
}

// androidActionMove is android.view.MotionEvent.ACTION_MOVE (2), used only
// to keep move events out of the log; the mapping itself takes the action
// Java sent and never assumes its value (see input.go's const block for
// where the number comes from).
const androidActionMove = 2
