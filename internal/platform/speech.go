package platform

type SpeechOptions struct {
	Language          string
	OnDevice          bool
	ContextualStrings []string
}
type SpeechResult struct {
	Text  string
	Final bool
	Err   error
}

// SpeechRecognizer runs on the main thread, as do its callbacks and recording
// controls. Results contain the complete transcript so far, not text deltas.
type SpeechRecognizer interface {
	Start(SpeechOptions, func(SpeechResult)) (SpeechRecording, error)
}
type SpeechRecording interface {
	Stop()   // finish capture and deliver a final result
	Cancel() // discard capture; do not deliver further results
}
type UnsupportedSpeech struct{}

func (UnsupportedSpeech) Start(SpeechOptions, func(SpeechResult)) (SpeechRecording, error) {
	return nil, ErrUnsupported
}
