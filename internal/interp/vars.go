package interp

import (
	"sort"
	"strings"
	"time"
)

// Var is one %variable.
type Var struct {
	Name  string
	Value string
	// UnsetAt, when non-zero, is when a -uN variable expires.
	UnsetAt time.Time
	// UnsetOnExit marks -e variables.
	UnsetOnExit bool
	seq         int
}

// Vars is a case-insensitive variable table that remembers insertion order,
// which $var(name,N) enumerates.
type Vars struct {
	m   map[string]*Var
	seq int
}

// NewVars returns an empty table.
func NewVars() *Vars { return &Vars{m: map[string]*Var{}} }

func varKey(name string) string { return strings.ToLower(strings.TrimPrefix(name, "%")) }

// Get returns a variable by name, with or without the % prefix.
func (vs *Vars) Get(name string) (*Var, bool) {
	v, ok := vs.m[varKey(name)]
	return v, ok
}

// Set creates or replaces a variable's value and returns it.
func (vs *Vars) Set(name, value string) *Var {
	k := varKey(name)
	if v, ok := vs.m[k]; ok {
		v.Value = value
		return v
	}
	vs.seq++
	v := &Var{Name: "%" + strings.TrimPrefix(name, "%"), Value: value, seq: vs.seq}
	vs.m[k] = v
	return v
}

// Unset removes variables matching a name, which may hold wildcards.
// It reports how many were removed.
func (vs *Vars) Unset(pattern string) int {
	if !strings.ContainsAny(pattern, "*?") {
		k := varKey(pattern)
		if _, ok := vs.m[k]; ok {
			delete(vs.m, k)
			return 1
		}
		return 0
	}
	n := 0
	for k, v := range vs.m {
		if WildMatch(pattern, v.Name, false) {
			delete(vs.m, k)
			n++
		}
	}
	return n
}

// Clear removes every variable.
func (vs *Vars) Clear() { vs.m = map[string]*Var{} }

// Match returns variables whose %name matches a wildcard, in creation order.
func (vs *Vars) Match(pattern string) []*Var {
	var out []*Var
	for _, v := range vs.m {
		if WildMatch(pattern, v.Name, false) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// All returns every variable in creation order.
func (vs *Vars) All() []*Var { return vs.Match("*") }

// Expire removes -uN variables whose time has come.
func (vs *Vars) Expire(now time.Time) {
	for k, v := range vs.m {
		if !v.UnsetAt.IsZero() && !now.Before(v.UnsetAt) {
			delete(vs.m, k)
		}
	}
}
