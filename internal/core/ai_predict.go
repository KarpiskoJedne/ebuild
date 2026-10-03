// Predictive failure analysis + adaptive scheduling hints.
//
// A transparent logistic model over local history (no network, no black
// box): recent failures, churn, and input size predict per-target failure
// probability; the scheduler consumes Hints to clamp concurrency and
// prioritize retries. All state persists as JSON in the workspace.
package core

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// History is the persisted per-target build record.
type History struct {
	Runs     int       `json:"runs"`
	Failures int       `json:"failures"`
	LastFail time.Time `json:"last_fail"`
	LastPass time.Time `json:"last_pass"`
	AvgMs    float64   `json:"avg_ms"`
	Churn    int       `json:"churn"` // src changes since last pass
}

// Store loads/saves history records under root/.ebuild/history.json.
type Store struct {
	path    string
	records map[string]*History
}

// OpenStore loads history (missing file = empty store, not an error).
func OpenStore(root string) (*Store, error) {
	s := &Store{path: filepath.Join(root, ".ebuild", "history.json"), records: map[string]*History{}}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &s.records); err != nil {
		return nil, err
	}
	return s, nil
}

// Record appends one outcome and persists the store.
func (s *Store) Record(target string, failed bool, durMs float64) error {
	h := s.records[target]
	if h == nil {
		h = &History{}
		s.records[target] = h
	}
	h.Runs++
	const alpha = 0.3 // EWMA for duration
	if h.AvgMs == 0 {
		h.AvgMs = durMs
	} else {
		h.AvgMs = alpha*durMs + (1-alpha)*h.AvgMs
	}
	if failed {
		h.Failures++
		h.LastFail = time.Now().UTC()
	} else {
		h.LastPass = time.Now().UTC()
		h.Churn = 0
	}
	return s.save()
}

// NoteChurn increments the change counter for a target (call on file events).
func (s *Store) NoteChurn(target string) {
	h := s.records[target]
	if h == nil {
		h = &History{}
		s.records[target] = h
	}
	h.Churn++
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s.records, "", "  ")
	return os.WriteFile(s.path, b, 0o644)
}

// FailureProb returns P(fail) in [0,1] from a logistic model:
// z = w0 + w1*failRate + w2*recency + w3*log1p(churn) + w4*log1p(avgMs).
func FailureProb(h *History) float64 {
	if h == nil || h.Runs == 0 {
		return 0.05 // cold start prior
	}
	failRate := float64(h.Failures) / float64(h.Runs)
	recency := 0.0
	if !h.LastFail.IsZero() && h.LastFail.After(h.LastPass) {
		recency = 1.0 / (1.0 + time.Since(h.LastFail).Hours()/24.0)
	}
	z := -3.0 + 4.0*failRate + 1.5*recency +
		0.4*math.Log1p(float64(h.Churn)) + 0.2*math.Log1p(h.AvgMs/1000.0)
	return 1.0 / (1.0 + math.Exp(-z))
}

// Hint is one adaptive-scheduling directive.
type Hint struct {
	Target   string
	ProbFail float64
	Action   string // "run-first" | "quarantine" | "clamp-concurrency" | "normal"
}

// Advise ranks targets by failure probability and emits hints.
func Advise(targets []string, s *Store, maxWorkers int) []Hint {
	var hints []Hint
	for _, t := range targets {
		p := FailureProb(s.records[t])
		h := Hint{Target: t, ProbFail: p, Action: "normal"}
		switch {
		case p >= 0.7:
			h.Action = "quarantine" // run alone, last
		case p >= 0.4:
			h.Action = "run-first" // fail fast
		}
		hints = append(hints, h)
	}
	sort.Slice(hints, func(i, j int) bool {
		if hints[i].Action == "run-first" && hints[j].Action != "run-first" {
			return true
		}
		return hints[i].ProbFail > hints[j].ProbFail
	})
	_ = maxWorkers
	return hints
}

// ClampWorkers reduces parallelism when many targets look risky.
func ClampWorkers(hints []Hint, maxWorkers int) int {
	risky := 0
	for _, h := range hints {
		if h.ProbFail >= 0.4 {
			risky++
		}
	}
	if risky > maxWorkers && maxWorkers > 1 {
		return maxWorkers / 2 // protect the machine; risky targets serialize
	}
	return maxWorkers
}
