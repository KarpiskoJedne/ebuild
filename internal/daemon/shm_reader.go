// Zero-copy reader for the daemon's shared-memory event ring.
//
// The daemon publishes file/build events into an mmap'd lock-free MPMC ring
// (see internal/daemon/shm_ring.rs). This reader maps the same file and
// consumes events with user-space atomics: no syscalls, no sockets, and no
// serialization on the hot path. (The file path is copied out per event —
// unavoidable in Go — but no new mappings or allocations of the ring occur.)
package daemon

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

const (
	shmMagic    = 0x4752444c49554245
	shmHeader   = 64
	shmSlotSize = 1024
	shmPathOff  = 12
	shmSeqOff   = 1016
	shmMaxPath  = 1008
)

// ShmReader consumes the shared ring. It is safe for a single consumer;
// create one reader per process.
type ShmReader struct {
	data  []byte
	slots uint64
	mask  uint64
	file  *os.File
}

// OpenShm maps an existing ring file published by the daemon.
func OpenShm(path string) (*ShmReader, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	m, err := mmapFile(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	if len(m) < shmHeader {
		munmapFile(m)
		f.Close()
		return nil, fmt.Errorf("ebuild: shm: truncated header")
	}
	if binary.LittleEndian.Uint64(m[0:8]) != shmMagic {
		munmapFile(m)
		f.Close()
		return nil, fmt.Errorf("ebuild: shm: bad magic")
	}
	slots := binary.LittleEndian.Uint64(m[8:16])
	if slots < 2 || slots&(slots-1) != 0 {
		munmapFile(m)
		f.Close()
		return nil, fmt.Errorf("ebuild: shm: bad slot count %d", slots)
	}
	if binary.LittleEndian.Uint64(m[16:24]) != shmSlotSize {
		munmapFile(m)
		f.Close()
		return nil, fmt.Errorf("ebuild: shm: bad slot size")
	}
	return &ShmReader{data: m, slots: slots, mask: slots - 1, file: f}, nil
}

// TryPop dequeues one event; ok=false when the ring is empty.
func (r *ShmReader) TryPop() (ev Event, ok bool) {
	tail := atomic.LoadUint64(r.tailPtr())
	for {
		cell := tail & r.mask
		seq := atomic.LoadUint64(r.seqPtr(cell))
		diff := int64(seq - (tail + 1))
		if diff == 0 {
			if !atomic.CompareAndSwapUint64(r.tailPtr(), tail, tail+1) {
				tail = atomic.LoadUint64(r.tailPtr())
				continue
			}
			break
		}
		if diff < 0 {
			if atomic.LoadUint64(r.headPtr()) <= tail {
				return Event{}, false
			}
			tail = atomic.LoadUint64(r.tailPtr())
			continue
		}
		tail = atomic.LoadUint64(r.tailPtr())
	}
	// Re-derive cell after the successful CAS (tail was claimed above).
	// tail here is the claimed position: recompute from CAS success value.
	// Note: tail variable still holds the claimed pos after break.
	cell := tail & r.mask
	base := shmHeader + int(cell)*shmSlotSize
	kind := binary.LittleEndian.Uint32(r.data[base : base+4])
	cookie := binary.LittleEndian.Uint32(r.data[base+4 : base+8])
	n := binary.LittleEndian.Uint32(r.data[base+8 : base+12])
	if n > shmMaxPath {
		n = shmMaxPath
	}
	ev = Event{Kind: kind, Cookie: cookie, Path: string(r.data[base+shmPathOff : base+shmPathOff+int(n)])}
	atomic.StoreUint64(r.seqPtr(cell), tail+r.slots)
	return ev, true
}

// Poll blocks up to timeout for the next event.
func (r *ShmReader) Poll(timeout time.Duration) (Event, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if ev, ok := r.TryPop(); ok {
			return ev, true
		}
		if time.Now().After(deadline) {
			return Event{}, false
		}
		time.Sleep(time.Millisecond)
	}
}

// Close unmaps the ring.
func (r *ShmReader) Close() error {
	munmapFile(r.data)
	r.data = nil
	return r.file.Close()
}

func (r *ShmReader) headPtr() *uint64 {
	return u64ptr(r.data[24:32])
}

func (r *ShmReader) tailPtr() *uint64 {
	return u64ptr(r.data[32:40])
}

func (r *ShmReader) seqPtr(cell uint64) *uint64 {
	off := shmHeader + int(cell)*shmSlotSize + shmSeqOff
	return u64ptr(r.data[off : off+8])
}
