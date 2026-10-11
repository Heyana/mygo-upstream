package ios

// displayWakeState belongs to the UI thread. Keep requested leases while
// inactive, but only inhibit the idle timer while the scene is active.
// This policy is plain Go so lifecycle and overlapping leases can be tested
// without UIKit; power.go supplies the native setter.
type displayWakeState struct {
	active, applied bool
	leases          int
	apply           func(bool)
}

func (s *displayWakeState) acquire(display bool) func() {
	if !display {
		return func() {} // iOS does not grant background execution here.
	}
	s.leases++
	s.update()
	live := true
	return func() {
		if live {
			live = false
			s.leases--
			s.update()
		}
	}
}

func (s *displayWakeState) setActive(active bool) {
	s.active = active
	s.update()
}

func (s *displayWakeState) update() {
	want := s.active && s.leases > 0
	if want != s.applied {
		s.applied = want
		if s.apply != nil {
			s.apply(want)
		}
	}
}
