//go:build windows

package daemon

import (
	"fmt"
	"os"
)

// The shm fast path is implemented for unix mmap; on Windows consumers use
// the UDS control-plane poll (PollEvent) until a MapViewOfFile reader lands.
func mmapFile(f *os.File) ([]byte, error) {
	return nil, fmt.Errorf("ebuild: shm: unsupported on windows (use PollEvent)")
}

func munmapFile(b []byte) {}

func u64ptr(b []byte) *uint64 { return nil }
