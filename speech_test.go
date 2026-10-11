//go:build !js

package mygo

import (
	"context"
	"errors"
	"github.com/egoist/mygo/internal/platform"
	"sync/atomic"
	"testing"
	"time"
)

type testSpeechProvider struct {
	emit      func(SpeechResult)
	recording testSpeechRecording
	options   SpeechOptions
	err       error
}
type testSpeechRecording struct{ stopped, canceled atomic.Int32 }

func (r *testSpeechRecording) Stop()   { r.stopped.Add(1) }
func (r *testSpeechRecording) Cancel() { r.canceled.Add(1) }
func (p *testSpeechProvider) Start(ctx context.Context, o SpeechOptions, emit func(SpeechResult)) (SpeechRecording, error) {
	p.emit = emit
	p.options = o
	return &p.recording, p.err
}
func speechFixture(t *testing.T) (*SpeechSession, *testSpeechProvider) {
	t.Helper()
	p := &testSpeechProvider{}
	if err := Speech.RegisterProvider(t.Name(), p); err != nil {
		t.Fatal(err)
	}
	s, err := Speech.Start(context.Background(), SpeechOptions{Provider: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Cancel)
	return s, p
}
func TestSpeechReplacesPartialAndRetainsFinal(t *testing.T) {
	s, p := speechFixture(t)
	p.emit(SpeechResult{Text: "first"})
	p.emit(SpeechResult{Text: "corrected"})
	p.emit(SpeechResult{Text: "final", Final: true})
	p.emit(SpeechResult{Text: "late"})
	r := <-s.Results()
	if r.Text != "final" || !r.Final {
		t.Fatalf("result = %+v", r)
	}
	if _, open := <-s.Results(); open {
		t.Fatal("final stream remained open")
	}
}
func TestSpeechStopCancelIdempotentAndDropsLateResults(t *testing.T) {
	s, p := speechFixture(t)
	s.Stop()
	s.Stop()
	p.emit(SpeechResult{Text: "provisional"})
	s.Cancel()
	s.Cancel()
	s.Stop()
	p.emit(SpeechResult{Text: "late", Final: true})
	r := <-s.Results()
	if !errors.Is(r.Err, context.Canceled) || r.Text != "" {
		t.Fatalf("cancel = %+v", r)
	}
	if p.recording.stopped.Load() != 1 || p.recording.canceled.Load() != 1 {
		t.Fatal("duplicate provider control")
	}
}
func TestSpeechDurationAndContextReleaseCapture(t *testing.T) {
	p := &testSpeechProvider{}
	Speech.RegisterProvider(t.Name(), p)
	ctx, cancel := context.WithCancel(context.Background())
	s, err := Speech.Start(ctx, SpeechOptions{Provider: t.Name(), MaxDuration: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for p.recording.stopped.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("capture duration not enforced")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case r := <-s.Results():
		if !errors.Is(r.Err, context.Canceled) {
			t.Fatal(r)
		}
	case <-time.After(time.Second):
		t.Fatal("context did not release capture")
	}
}
func TestSpeechFailedStartReleasesProvider(t *testing.T) {
	p := &testSpeechProvider{err: errors.New("start failed")}
	Speech.RegisterProvider(t.Name(), p)
	if s, err := Speech.Start(context.Background(), SpeechOptions{Provider: t.Name()}); s != nil || err == nil {
		t.Fatal("failed start accepted")
	}
	if p.recording.canceled.Load() != 1 {
		t.Fatal("failed capture leaked")
	}
}
func TestSpeechRejectsInvalidOptions(t *testing.T) {
	for _, o := range []SpeechOptions{{MaxDuration: -1}, {MaxDuration: 11 * time.Minute}, {Provider: "missing"}} {
		if _, err := Speech.Start(context.Background(), o); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	if _, err := Speech.Start(nil, SpeechOptions{}); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestAppleSpeechAuthorizationAndMainThreadControls(t *testing.T) {
	recording := &testSpeechRecording{}
	var emit func(platform.SpeechResult)
	onMain(func() {
		fb.PermissionStatuses = nil
		fb.PermissionDecision = "denied"
		fb.SpeechStart = func(o platform.SpeechOptions, callback func(platform.SpeechResult)) (platform.SpeechRecording, error) {
			if !isMainThread() {
				t.Error("speech preparation left main thread")
			}
			if !o.OnDevice || o.Language != "zh-CN" {
				t.Errorf("native options lost: %+v", o)
			}
			emit = callback
			return recording, nil
		}
	})
	t.Cleanup(func() {
		onMain(func() { fb.PermissionStatuses = nil; fb.PermissionDecision = ""; fb.SpeechStart = nil })
	})
	if _, err := Speech.Start(context.Background(), SpeechOptions{OnDevice: true}); err == nil {
		t.Fatal("permission denial accepted")
	}
	if emit != nil {
		t.Fatal("capture started without authorization")
	}
	onMain(func() { fb.PermissionStatuses = nil; fb.PermissionDecision = "granted" })
	s, err := Speech.Start(context.Background(), SpeechOptions{OnDevice: true, Language: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	s.Stop()
	s.Stop()
	onMain(func() { emit(platform.SpeechResult{Text: "你好", Final: true}) })
	if r := <-s.Results(); r.Text != "你好" || !r.Final {
		t.Fatal(r)
	}
	if recording.stopped.Load() != 1 {
		t.Fatal("native Stop duplicated")
	}
}
func TestSpeechAuthorizationHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Speech.Authorize(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
