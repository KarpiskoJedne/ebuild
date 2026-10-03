// stdlib ANSI terminal dashboard: live Gantt execution chart, core
// utilization, and cache-hit counters. The Model/Update/View shape mirrors
// Bubble Tea so a Bubble Tea frontend can wrap it without changes.
package core

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Bar is one Gantt row: a task lane with start/end and status.
type Bar struct {
	Label  string
	Target string
	Start  time.Time
	End    time.Time
	Failed bool
	Cached bool
}

// Model is the dashboard state (mutex-guarded for scheduler goroutines).
type Model struct {
	mu      sync.Mutex
	bars    []Bar
	hits    int64
	misses  int64
	workers int
	res     Resources
	updated time.Time
}

// NewModel creates a dashboard model for n workers.
func NewModel(workers int) *Model {
	return &Model{workers: workers, updated: time.Now()}
}

// Update records a finished result as a Gantt bar + cache counters.
func (m *Model) Update(r Result) {
	m.mu.Lock()
	defer m.mu.Unlock()
	end := time.Now()
	m.bars = append(m.bars, Bar{
		Label: r.ID, Target: r.Target,
		Start: end.Add(-r.Duration), End: end,
		Failed: r.Err != nil, Cached: r.Cached,
	})
	if r.Cached {
		m.hits++
	} else {
		m.misses++
	}
	if len(m.bars) > 200 {
		m.bars = append([]Bar{}, m.bars[len(m.bars)-200:]...)
	}
	m.updated = end
}

// SetResources refreshes the hardware snapshot row.
func (m *Model) SetResources(res Resources) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.res = res
}

// View renders the full dashboard as ANSI text (CR-clear + explicit newlines;
// callers print it on each tick).
func (m *Model) View() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	b.WriteString("\x1b[H\x1b[2J") // home + clear
	fmt.Fprintf(&b, "ebuild live — workers=%d cpus=%d load=%.2f mem=%d/%dMB  cache hits=%d miss=%d\n",
		m.workers, m.res.CPUs, m.res.Load1, m.res.MemAvailMB, m.res.MemTotalMB, m.hits, m.misses)
	b.WriteString(strings.Repeat("─", 72) + "\n")
	bars := append([]Bar{}, m.bars...)
	sort.Slice(bars, func(i, j int) bool { return bars[i].Start.Before(bars[j].Start) })
	if len(bars) > 20 {
		bars = bars[len(bars)-20:]
	}
	var t0 time.Time
	for _, br := range bars {
		if t0.IsZero() || br.Start.Before(t0) {
			t0 = br.Start
		}
	}
	span := time.Since(t0)
	if span <= 0 {
		span = time.Second
	}
	for _, br := range bars {
		off := int(float64(br.Start.Sub(t0)) / float64(span) * 48)
		w := int(float64(br.End.Sub(br.Start)) / float64(span) * 48)
		if w < 1 {
			w = 1
		}
		if off < 0 {
			off = 0
		}
		if off > 48 {
			off = 48
		}
		mark := "■"
		switch {
		case br.Failed:
			mark = "\x1b[31m■\x1b[0m" // red
		case br.Cached:
			mark = "\x1b[36m■\x1b[0m" // cyan
		default:
			mark = "\x1b[32m■\x1b[0m" // green
		}
		label := br.Target
		if len(label) > 20 {
			label = label[:20]
		}
		fmt.Fprintf(&b, "%-20s │%s%s%s %s\n", label,
			strings.Repeat(" ", off), strings.Repeat(mark, w),
			strings.Repeat(" ", max0(48-off-w)), br.End.Sub(br.Start).Truncate(time.Millisecond))
	}
	fmt.Fprintf(&b, "updated %s\n", m.updated.Format("15:04:05"))
	return b.String()
}

func max0(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
