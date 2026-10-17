//go:build android && cgo

package backend

// The secure store: platform.SecureStorage over the Java side's Keystore.
//
// The Keystore holds one key of this app's, made on first use and kept in the
// hardware where the device has one; the entries are ciphertexts of it, kept in
// the app's shared preferences. That is what Android offers, and it is why Get
// can fail where Set once succeeded: a key is invalidated when the lock screen
// is removed or the app's data is restored to another device, and the entries it
// encrypted are then unreadable. The store reports that
// (platform.ErrSecretNotFound, with the reason in logcat) rather than returning
// garbage.
//
// Every call runs the Keystore off the pump and calls back through Post: an
// unwrap of a hardware key is milliseconds, and the pump draws frames, so
// blocking it there would stall the window the value is being read for.

import (
	"github.com/egoist/mygo/internal/platform"
)

// SecureStorage returns the store the Java side implements.
func (b *Backend) SecureStorage() platform.SecureStorage { return androidSecureStorage{b: b} }

type androidSecureStorage struct{ b *Backend }

// Set stores value. accessibility is accepted and not narrowed: the Keystore
// has no per-item lifetime — an entry is readable whenever the device is
// unlocked — so platform's vocabulary has nothing to choose between here.
func (s androidSecureStorage) Set(namespace, key string, value []byte, accessibility string, done func(error)) {
	s.run(done, func(bridge PlatformBridge) error {
		return bridge.SecretSet(namespace, key, value, accessibility)
	})
}

// Get reads value; a missing or unreadable one is platform.ErrSecretNotFound.
func (s androidSecureStorage) Get(namespace, key string, done func([]byte, error)) {
	bridge := platformBridge()
	if bridge == nil {
		s.b.Post(func() { done(nil, ErrPlatformBridge) })
		return
	}
	go func() {
		value, err := bridge.SecretGet(namespace, key)
		s.b.Post(func() { done(value, err) })
	}()
}

// Delete removes value; a missing one is not an error.
func (s androidSecureStorage) Delete(namespace, key string, done func(error)) {
	s.run(done, func(bridge PlatformBridge) error {
		return bridge.SecretDelete(namespace, key)
	})
}

// run is Set and Delete: the call off the pump, then done with its result on
// the pump.
func (s androidSecureStorage) run(done func(error), fn func(PlatformBridge) error) {
	bridge := platformBridge()
	if bridge == nil {
		s.b.Post(func() { done(ErrPlatformBridge) })
		return
	}
	go func() {
		err := fn(bridge)
		s.b.Post(func() { done(err) })
	}()
}
