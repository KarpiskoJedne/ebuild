// Dependency graph queries: transitive deps, reverse deps, critical path,
// and change-impact analysis over the workspace target graph.
package core

import "sort"
import "strings"

// NormalizeTargetRef expands shorthand references to canonical form:
// `//pkg` → `//pkg:base` (package default target, bazel-style),
// `:name` → `//:name` (root package). Full refs and `//...` pass through.
func NormalizeTargetRef(s string) string {
	if strings.HasPrefix(s, ":") && !strings.Contains(s[1:], ":") {
		return "//" + s
	}
	if strings.HasPrefix(s, "//") {
		rest := s[2:]
		if rest == "" || rest == "..." || strings.Contains(rest, ":") || strings.Contains(rest, "...") {
			return s
		}
		if i := strings.LastIndex(rest, "/"); i >= 0 {
			return s + ":" + rest[i+1:]
		}
		return s + ":" + rest
	}
	return s
}

// Graph is an adjacency index over targets by name.
type Graph struct {
	byName map[string]*Target
	deps   map[string][]string
	rdeps  map[string][]string
}

// NewGraph indexes targets; unknown dep names are kept as dangling edges.
func NewGraph(targets []Target) *Graph {
	g := &Graph{byName: map[string]*Target{}, deps: map[string][]string{}, rdeps: map[string][]string{}}
	for i := range targets {
		t := &targets[i]
		g.byName[t.Name] = t
		g.deps[t.Name] = append([]string{}, t.Deps...)
		for _, d := range t.Deps {
			g.rdeps[d] = append(g.rdeps[d], t.Name)
		}
	}
	return g
}

// TopoSort orders targets so dependencies come first (Kahn's algorithm).
// Cycles are broken deterministically: members are appended in name order.
func TopoSort(targets []Target) []Target {
	indeg := map[string]int{}
	byName := map[string]Target{}
	for _, t := range targets {
		byName[t.Name] = t
	}
	for _, t := range targets {
		if _, ok := indeg[t.Name]; !ok {
			indeg[t.Name] = 0
		}
		for _, d := range t.Deps {
			if _, ok := byName[d]; ok {
				indeg[t.Name]++
			}
		}
	}
	rdeps := map[string][]string{}
	for _, t := range targets {
		for _, d := range t.Deps {
			if _, ok := byName[d]; ok {
				rdeps[d] = append(rdeps[d], t.Name)
			}
		}
	}
	var ready []string
	for n, d := range indeg {
		if d == 0 {
			ready = append(ready, n)
		}
	}
	sort.Strings(ready)
	var out []Target
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		out = append(out, byName[n])
		for _, m := range rdeps[n] {
			indeg[m]--
			if indeg[m] == 0 {
				ready = append(ready, m)
			}
		}
		sort.Strings(ready)
	}
	if len(out) < len(targets) {
		// Cycle: append the remainder in sorted order (deterministic).
		seen := map[string]bool{}
		for _, t := range out {
			seen[t.Name] = true
		}
		var rest []string
		for _, t := range targets {
			if !seen[t.Name] {
				rest = append(rest, t.Name)
			}
		}
		sort.Strings(rest)
		for _, n := range rest {
			out = append(out, byName[n])
		}
	}
	return out
}

// TransitiveDeps returns the sorted closure of dependencies of name.
func (g *Graph) TransitiveDeps(name string) []string {
	seen := map[string]bool{name: true}
	stack := append([]string{}, g.deps[name]...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		stack = append(stack, g.deps[n]...)
	}
	delete(seen, name)
	return sortedKeys(seen)
}

// ReverseDeps returns the sorted closure of dependents of name.
func (g *Graph) ReverseDeps(name string) []string {
	seen := map[string]bool{name: true}
	stack := append([]string{}, g.rdeps[name]...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		stack = append(stack, g.rdeps[n]...)
	}
	delete(seen, name)
	return sortedKeys(seen)
}

// CriticalPath returns the longest dependency chain ending at name,
// weighted by per-target cost (default 1). Ties break alphabetically.
func (g *Graph) CriticalPath(name string, cost map[string]int) []string {
	memo := map[string][]string{}
	var visit func(n string, trail map[string]bool) []string
	visit = func(n string, trail map[string]bool) []string {
		if p, ok := memo[n]; ok {
			return p
		}
		if trail[n] {
			return []string{n}
		}
		trail[n] = true
		best := []string{}
		bestCost := -1
		deps := append([]string{}, g.deps[n]...)
		sort.Strings(deps)
		for _, d := range deps {
			p := visit(d, trail)
			c := pathCost(p, cost)
			if c > bestCost {
				bestCost = c
				best = p
			}
		}
		delete(trail, n)
		out := append(append([]string{}, best...), n)
		memo[n] = out
		return out
	}
	return visit(name, map[string]bool{})
}

// Impact maps changed files to affected target names via srcs/rdeps.
func (g *Graph) Impact(targets []Target, changed []string) []string {
	hit := map[string]bool{}
	for _, f := range changed {
		for i := range targets {
			for _, s := range targets[i].Srcs {
				if s == f {
					hit[targets[i].Name] = true
				}
			}
		}
	}
	// Propagate to reverse dependents.
	queue := sortedKeys(hit)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, r := range g.rdeps[n] {
			if !hit[r] {
				hit[r] = true
				queue = append(queue, r)
			}
		}
	}
	return sortedKeys(hit)
}

func pathCost(p []string, cost map[string]int) int {
	c := 0
	for _, n := range p {
		if v, ok := cost[n]; ok {
			c += v
		} else {
			c++
		}
	}
	return c
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
