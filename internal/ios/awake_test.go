package ios

import (
	"reflect"
	"testing"
)

func TestDisplayWakeLeasesAndLifecycle(t *testing.T) {
	var changes []bool
	s := displayWakeState{apply: func(on bool) { changes = append(changes, on) }}
	a := s.acquire(true)
	if len(changes) != 0 {
		t.Fatal("inactive acquisition disabled idle timer")
	}
	s.setActive(true)
	b := s.acquire(true)
	a()
	a()
	if !s.applied || s.leases != 1 {
		t.Fatal("releasing one lease canceled another")
	}
	s.setActive(false)
	s.setActive(false)
	s.setActive(true)
	b()
	s.setActive(false)
	s.setActive(true)
	if s.applied || s.leases != 0 || !reflect.DeepEqual(changes, []bool{true, false, true, false}) {
		t.Fatalf("idle timer state leaked across lifecycle: state=%+v changes=%v", s, changes)
	}
}

func TestDisplayWakeReleaseInBackgroundAndSystemOnly(t *testing.T) {
	s := displayWakeState{}
	s.setActive(true)
	s.acquire(false)()
	if s.applied || s.leases != 0 {
		t.Fatal("system-only request prevented screen idle")
	}
	off := s.acquire(true)
	s.setActive(false)
	off()
	s.setActive(true)
	if s.applied || s.leases != 0 {
		t.Fatal("released background lease was restored")
	}
}
