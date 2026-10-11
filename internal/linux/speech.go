//go:build linux

package linux

import "github.com/egoist/mygo/internal/platform"

func (*Backend) Speech() platform.SpeechRecognizer { return platform.UnsupportedSpeech{} }
