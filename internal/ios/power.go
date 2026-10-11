//go:build ios && cgo

package ios

/*
#include "native.h"
*/
import "C"

import "github.com/egoist/mygo/internal/platform"

type power struct {
	platform.Power
	b *Backend
}

func (b *Backend) Power() platform.Power { return power{b.Backend.Power(), b} }

func (p power) KeepAwake(display bool, _ string) func() {
	return p.b.awake.acquire(display)
}
