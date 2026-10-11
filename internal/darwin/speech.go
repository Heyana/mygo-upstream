//go:build darwin && !ios

package darwin

import "github.com/egoist/mygo/internal/platform"

func (*Backend) Speech() platform.SpeechRecognizer { return platform.UnsupportedSpeech{} }
