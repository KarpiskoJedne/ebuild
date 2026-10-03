//go:build !windows

package daemon

import (
	"os"
	"syscall"
	"unsafe"
)

func mmapFile(f *os.File) ([]byte, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	n := int(fi.Size())
	if n <= 0 {
		n = shmHeader
	}
	b, err := syscall.Mmap(int(f.Fd()), 0, n, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func munmapFile(b []byte) {
	if len(b) > 0 {
		_ = syscall.Munmap(b)
	}
}

// u64ptr reinterprets an 8-byte slice as a *uint64 for atomics.
// The daemon guarantees 8-byte alignment of all atomic fields.
func u64ptr(b []byte) *uint64 {
	return (*uint64)(unsafe.Pointer(&b[0]))
}
