package fake

import "github.com/egoist/mygo/internal/platform"

type speechRecognizer struct{ b *Backend }

func (b *Backend) Speech() platform.SpeechRecognizer { return speechRecognizer{b} }
func (s speechRecognizer) Start(o platform.SpeechOptions, emit func(platform.SpeechResult)) (platform.SpeechRecording, error) {
	if s.b.SpeechStart != nil {
		return s.b.SpeechStart(o, emit)
	}
	return nil, platform.ErrUnsupported
}
