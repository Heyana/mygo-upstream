//go:build windows

package windows

import "github.com/egoist/mygo/internal/platform"

func (*Backend) Speech() platform.SpeechRecognizer { return platform.UnsupportedSpeech{} }
