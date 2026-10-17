//go:build android || linux || darwin

package text

import (
	"os"
	"syscall"
)

// mapFile maps a file read-only and returns its bytes. The pages come from
// the kernel as they are touched and can be taken back under pressure, so a
// large font file costs its working set rather than its size — see
// parseFontFile for why that matters on Android.
//
// The mapping is never unmapped: a caller holds it for as long as the faces
// parsed out of it live, and this package holds those for the life of the
// process. Unmapping it while a face pointed into it would be a fault, not
// a leak fixed.
func mapFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	n := int(st.Size())
	if n <= 0 {
		return nil, os.ErrInvalid
	}
	return syscall.Mmap(int(f.Fd()), 0, n, syscall.PROT_READ, syscall.MAP_PRIVATE)
}
