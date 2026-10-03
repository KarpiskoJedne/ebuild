// Distributed CAS garbage collection: LRU eviction over the local CAS
// directory with pinned-digest protection and age-based sweeping.
package daemon

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Entry is one CAS object on disk.
type Entry struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// ScanCAS lists regular files under dir (layout: dir/xx/<hash> shards).
func ScanCAS(dir string) ([]Entry, error) {
	var out []Entry
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, Entry{Path: p, Size: fi.Size(), ModTime: fi.ModTime()})
		return nil
	})
	return out, err
}

// SweepOld deletes objects older than maxAge; returns the removed count.
func SweepOld(dir string, maxAge time.Duration) (int, error) {
	entries, err := ScanCAS(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	n := 0
	for _, e := range entries {
		if e.ModTime.Before(cutoff) {
			if err := os.Remove(e.Path); err == nil {
				n++
			}
		}
	}
	return n, nil
}

// Collect enforces a byte budget: evicts least-recently-used objects first
// (by mtime) until usage <= maxBytes. Pinned digests (by filename) are kept.
// Returns bytes freed and files removed.
func Collect(dir string, maxBytes int64, pinned map[string]bool) (int64, int, error) {
	entries, err := ScanCAS(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	var total int64
	for _, e := range entries {
		total += e.Size
	}
	if total <= maxBytes {
		return 0, 0, nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ModTime.Before(entries[j].ModTime) })
	var freed int64
	removed := 0
	for _, e := range entries {
		if total-freed <= maxBytes {
			break
		}
		if pinned[filepath.Base(e.Path)] {
			continue
		}
		if err := os.Remove(e.Path); err != nil {
			continue
		}
		freed += e.Size
		removed++
	}
	PruneEmptyDirs(dir)
	return freed, removed, nil
}

// PruneEmptyDirs removes empty shard directories (two levels max).
func PruneEmptyDirs(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		kids, err := os.ReadDir(sub)
		if err != nil || len(kids) > 0 {
			continue
		}
		_ = os.Remove(sub)
	}
}
