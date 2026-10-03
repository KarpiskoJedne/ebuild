// Hardware-aware scheduler: dynamic worker scaling, bounded backpressure,
// and JSONL tracing spans (OTLP-shaped: trace/span IDs, timestamps, attrs).
package core

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Task is one scheduled action execution.
type Task struct {
	ID     string
	Action Action
	Env    []string
	Weight int // relative cost for critical-path ordering
}

// Result records a finished task.
type Result struct {
	ID       string
	Target   string
	Err      error
	Duration time.Duration
	Cached   bool
	// Output is the tail (last 8 KiB) of combined stdout+stderr. It is
	// always captured so FAIL lines can show the compiler/test log.
	Output string
}

// maxResultOutput caps captured per-task output.
const maxResultOutput = 8 * 1024

// Span is a single tracing span exported as JSONL.
type Span struct {
	TraceID string            `json:"trace_id"`
	SpanID  string            `json:"span_id"`
	Name    string            `json:"name"`
	StartUs int64             `json:"start_us"`
	EndUs   int64             `json:"end_us"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// Resources is a point-in-time hardware snapshot.
type Resources struct {
	CPUs       int
	MemTotalMB uint64
	MemAvailMB uint64
	Load1      float64
	At         time.Time
}

// Scheduler runs tasks with adaptive concurrency and backpressure.
type Scheduler struct {
	maxWorkers int
	queue      chan Task
	results    chan Result
	tracer     *json.Encoder
	mu         sync.Mutex
	pending    int64
	done       int64
	failed     int64
	traceFile  *os.File
}

// NewScheduler creates a scheduler; workers<=0 means NumCPU, tracePath==""
// disables tracing.
func NewScheduler(workers int, queueDepth int, tracePath string) (*Scheduler, error) {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > 256 {
		workers = 256
	}
	if queueDepth <= 0 {
		queueDepth = 1024
	}
	s := &Scheduler{
		maxWorkers: workers,
		queue:      make(chan Task, queueDepth),
		results:    make(chan Result, queueDepth),
	}
	if tracePath != "" {
		if err := os.MkdirAll(filepath.Dir(tracePath), 0o755); err != nil {
			return nil, err
		}
		f, err := os.Create(tracePath)
		if err != nil {
			return nil, err
		}
		s.traceFile = f
		s.tracer = json.NewEncoder(f)
	}
	return s, nil
}

// SampleResources reads CPU count, Go heap stats, and /proc/loadavg on Linux.
func SampleResources() Resources {
	r := Resources{CPUs: runtime.NumCPU(), At: time.Now().UTC()}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	// Best-effort totals from cgroup or /proc/meminfo; fall back to heap.
	total, avail := readMemInfo()
	r.MemTotalMB = total
	r.MemAvailMB = avail
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f, err := strconv.ParseFloat(strings.Fields(string(b))[0], 64); err == nil {
			r.Load1 = f
		}
	}
	_ = ms
	return r
}

func readMemInfo() (totalMB, availMB uint64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	vals := map[string]uint64{}
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		vals[strings.TrimSuffix(f[0], ":")] = v // kB
	}
	return vals["MemTotal"] / 1024, vals["MemAvailable"] / 1024
}

// EffectiveWorkers scales down under memory pressure or high load.
func (s *Scheduler) EffectiveWorkers(res Resources) int {
	w := s.maxWorkers
	if res.MemTotalMB > 0 && res.MemAvailMB*4 < res.MemTotalMB && w > 1 {
		w = w / 2 // less than 25% RAM free: halve parallelism
	}
	if res.Load1 > float64(res.CPUs)*2 && w > 1 {
		w-- // heavily oversubscribed host: yield one lane
	}
	if w < 1 {
		w = 1
	}
	return w
}

// Submit enqueues a task; it blocks when the bounded queue is full
// (backpressure) or returns ctx.Err() on cancellation.
func (s *Scheduler) Submit(ctx context.Context, t Task) error {
	atomic.AddInt64(&s.pending, 1)
	select {
	case s.queue <- t:
		return nil
	case <-ctx.Done():
		atomic.AddInt64(&s.pending, -1)
		return ctx.Err()
	}
}

// Run executes queued tasks until ctx ends or Close is called. Each task runs
// via os/exec with the provided hermetic Env; /dev/null stdio by default.
func (s *Scheduler) Run(ctx context.Context, res Resources) {
	n := s.EffectiveWorkers(res)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case t, ok := <-s.queue:
					if !ok {
						return
					}
					s.execOne(ctx, t)
				}
			}
		}()
	}
	wg.Wait()
}

// Close drains: no more submits; running workers finish.
func (s *Scheduler) Close() {
	close(s.queue)
	if s.traceFile != nil {
		s.traceFile.Close()
	}
}

// Stats returns pending/done/failed counters.
func (s *Scheduler) Stats() (pending, done, failed int64) {
	return atomic.LoadInt64(&s.pending), atomic.LoadInt64(&s.done), atomic.LoadInt64(&s.failed)
}

func (s *Scheduler) execOne(ctx context.Context, t Task) {
	start := time.Now()
	span := s.beginSpan(t)
	var runErr error
	var output string
	if t.Action.Argv != "" {
		// Ensure the output directory exists (Action.Target doubles as the
		// output path for compile rules).
		if d := filepath.Dir(t.Action.Target); d != "." && d != "" {
			outDir := d
			if !filepath.IsAbs(outDir) {
				outDir = filepath.Join(t.Action.WorkDir, d)
			}
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				runErr = err
			}
		}
		if runErr == nil {
			parts, err := SplitCommand(t.Action.Argv)
			if err != nil {
				runErr = err
			} else if len(parts) == 0 {
				runErr = fmt.Errorf("empty command")
			} else {
				cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
				cmd.Dir = t.Action.WorkDir
				cmd.Env = t.Env
				var buf bytes.Buffer
				cmd.Stdout = &buf
				cmd.Stderr = &buf
				runErr = cmd.Run()
				output = tailString(buf.String(), maxResultOutput)
			}
			if runErr == nil {
				runErr = finishActionOutput(t.Action)
			}
		}
	}
	dur := time.Since(start)
	if runErr != nil {
		atomic.AddInt64(&s.failed, 1)
	} else {
		atomic.AddInt64(&s.done, 1)
	}
	atomic.AddInt64(&s.pending, -1)
	s.endSpan(span, t, runErr)
	select {
	case s.results <- Result{ID: t.ID, Target: t.Action.Target, Err: runErr, Duration: dur, Output: output}:
	default:
	}
}

// Results exposes the finished-task channel to TUI/AI consumers.
func (s *Scheduler) Results() <-chan Result { return s.results }

// tailString returns the last n bytes of s (whole string if shorter).
func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// RunPlan executes actions in dependency waves: an action runs only after
// every path in its Needs set has been produced by an earlier wave. Actions
// whose dependencies failed are skipped and reported. Successful actions
// have their Target verified (targets without ":" are output paths that
// must exist afterwards). History is recorded per action.
func RunPlan(root string, actions []Action, jobs int) ([]Result, error) {
	res := SampleResources()
	sched, err := NewScheduler(jobs, 1024, filepath.Join(root, ".ebuild", "trace.jsonl"))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx, res)
	store, _ := OpenStore(root)
	defer sched.Close()

	var results []Result
	done := map[string]bool{}   // produced output paths
	failed := map[string]bool{} // failed output paths
	pending := append([]Action{}, actions...)
	deadline := time.After(30 * time.Minute)
	nseq := 0
	for len(pending) > 0 {
		var ready, rest []Action
		for _, a := range pending {
			blocked := false
			unmet := false
			for _, n := range a.Needs {
				if failed[n] {
					blocked = true
					break
				}
				if !done[n] {
					unmet = true
				}
			}
			switch {
			case blocked:
				results = append(results, Result{ID: fmt.Sprintf("skip%04d", nseq), Target: a.Target,
					Err: fmt.Errorf("skipped: dependency failed")})
				nseq++
			case unmet:
				rest = append(rest, a)
			default:
				ready = append(ready, a)
			}
		}
		pending = rest
		if len(ready) == 0 {
			if len(pending) == 0 {
				break
			}
			var names []string
			for _, a := range pending {
				var missing []string
				for _, n := range a.Needs {
					if !done[n] {
						missing = append(missing, n)
					}
				}
				names = append(names, fmt.Sprintf("%s (missing %s)", a.Target, strings.Join(missing, ", ")))
			}
			return results, fmt.Errorf("ebuild: deadlocked plan, unmet dependencies: %s", strings.Join(names, "; "))
		}
		for _, a := range ready {
			nseq++
			t := Task{ID: fmt.Sprintf("t%04d", nseq), Action: a, Env: HermeticEnv(nil, nil)}
			select {
			case sched.queue <- t:
			case <-ctx.Done():
				return results, ctx.Err()
			case <-deadline:
				return results, fmt.Errorf("ebuild: build timed out")
			}
		}
		for range ready {
			select {
			case r := <-sched.Results():
				if store != nil {
					_ = store.Record(r.Target, r.Err != nil, float64(r.Duration.Milliseconds()))
				}
				results = append(results, r)
				if r.Err != nil {
					failed[r.Target] = true
				} else {
					done[r.Target] = true
				}
			case <-deadline:
				return results, fmt.Errorf("ebuild: build timed out")
			}
		}
	}
	var errs int
	for _, r := range results {
		if r.Err != nil {
			errs++
		}
	}
	if errs > 0 {
		return results, fmt.Errorf("ebuild: %d/%d actions failed", errs, len(actions))
	}
	return results, nil
}

// finishActionOutput settles a successful action's outputs:
//
//   - `.ok` stamp targets are completion markers (make/ninja-style): the
//     scheduler creates the empty stamp itself, portably, instead of
//     shelling out to `touch`. Their presence proves the step ran.
//   - logical target refs (containing ":") are daemon-expanded: exempt.
//   - everything else is a concrete artifact that must exist on disk,
//     resolved against the action workdir.
func finishActionOutput(a Action) error {
	if !filepath.IsAbs(a.Target) && strings.Contains(a.Target, ":") {
		return nil
	}
	p := a.Target
	if !filepath.IsAbs(p) {
		p = filepath.Join(a.WorkDir, p)
	}
	if strings.HasSuffix(p, ".ok") {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(p, []byte{}, 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("produced no output at %s", a.Target)
	}
	return nil
}

func newID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Scheduler) beginSpan(t Task) *Span {
	if s.tracer == nil {
		return nil
	}
	return &Span{
		TraceID: newID(16),
		SpanID:  newID(8),
		Name:    "build/" + t.Action.Rule,
		StartUs: time.Now().UnixMicro(),
		Attrs:   map[string]string{"target": t.Action.Target, "task": t.ID},
	}
}

func (s *Scheduler) endSpan(span *Span, t Task, runErr error) {
	if s.tracer == nil || span == nil {
		return
	}
	span.EndUs = time.Now().UnixMicro()
	if runErr != nil {
		if span.Attrs == nil {
			span.Attrs = map[string]string{}
		}
		span.Attrs["error"] = runErr.Error()
	}
	s.mu.Lock()
	_ = s.tracer.Encode(span)
	s.mu.Unlock()
}
