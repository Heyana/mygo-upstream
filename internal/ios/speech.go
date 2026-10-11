//go:build ios && cgo

package ios

/*
#include "native.h"
*/
import "C"

import (
	"encoding/json"
	"errors"
	"github.com/egoist/mygo/internal/platform"
)

// All access is on the UIKit main thread, including native result delivery.
var speechRequests = map[uint64]func(platform.SpeechResult){}
var nextSpeechRequest uint64

type speechRecognizer struct{}
type speechRecording struct{ id uint64 }

func (*Backend) Speech() platform.SpeechRecognizer { return speechRecognizer{} }
func (speechRecognizer) Start(o platform.SpeechOptions, emit func(platform.SpeechResult)) (platform.SpeechRecording, error) {
	data, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	nextSpeechRequest++
	id := nextSpeechRequest
	speechRequests[id] = emit
	var message string
	cString(string(data), func(p *C.char) { message = ownedString(C.mygo_ios_speech_start(C.uint64_t(id), p)) })
	if message != "" {
		delete(speechRequests, id)
		return nil, errors.New(message)
	}
	return speechRecording{id}, nil
}
func (r speechRecording) Stop() { C.mygo_ios_speech_stop(C.uint64_t(r.id)) }
func (r speechRecording) Cancel() {
	delete(speechRequests, r.id)
	C.mygo_ios_speech_cancel(C.uint64_t(r.id))
}

//export goIOSSpeechResult
func goIOSSpeechResult(token C.uint64_t, text *C.char, final C.bool, message *C.char) {
	emit := speechRequests[uint64(token)]
	if emit == nil {
		return
	}
	r := platform.SpeechResult{Text: C.GoString(text), Final: bool(final)}
	if message != nil && C.GoString(message) != "" {
		r.Err = errors.New(C.GoString(message))
		r.Final = true
	}
	if r.Final {
		delete(speechRequests, uint64(token))
	}
	emit(r)
}
