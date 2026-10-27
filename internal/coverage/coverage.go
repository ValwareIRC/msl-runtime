// Package coverage measures how much of the mIRC inventory a runtime implements.
package coverage

import (
	"sort"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/spec"
)

// Language lists inventory entries implemented by the evaluator itself rather
// than as registered builtins.
var Language = map[string]bool{
	"$": true, "$$": true, "$!": true, "$&": true, "$(...)": true, "$+": true,
	"$0": true, "$1-": true, "$?": true, "$*": true, "#": true, "%": true,
	"/if": true, "/while": true, "//": true, "/elseif": true, "/else": true,
	"CTCP prefix": true, "Ctcp Events": true, "Ctcp finger reply": true, "RAW prefix": true, "Raw events": true,
}

// Entry is one inventory entry with its implementation state.
type Entry struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Page        string `json:"page"`
	Scope       string `json:"scope"`
	Reason      string `json:"reason,omitempty"`
	Syntax      string `json:"syntax,omitempty"`
	Implemented bool   `json:"implemented"`
}

// Counts summarises one kind.
type Counts struct {
	InScope     int `json:"inScope"`
	Implemented int `json:"implemented"`
	Excluded    int `json:"excluded"`
}

// Report is the result of Compute.
type Report struct {
	Entries []Entry           `json:"entries"`
	ByKind  map[string]Counts `json:"byKind"`
	// Extra are registered names with no inventory entry.
	Extra []string `json:"extra"`
}

// Events is implemented by packages that dispatch on-events, so coverage can
// see which events are handled.
type Events interface{ HandledEvents() []string }

// Compute compares a runtime's registrations against the inventory. events
// may be nil.
func Compute(rt *interp.Runtime, inv spec.Inventory, events Events) Report {
	idents := set(rt.Idents())
	cmds := set(rt.Commands())
	evs := map[string]bool{}
	if events != nil {
		evs = set(events.HandledEvents())
	}

	r := Report{ByKind: map[string]Counts{}}
	seen := map[string]bool{}
	for _, e := range inv.Entries {
		if e.Kind == spec.KindTopic {
			continue
		}
		key, impl := lookup(e, idents, cmds, evs)
		seen[key] = true
		ce := Entry{
			Name: e.Name, Kind: e.Kind, Page: e.Refs[0].Page, Scope: e.Scope.Scope,
			Reason: e.Scope.Reason, Syntax: e.Refs[0].Syntax, Implemented: impl,
		}
		r.Entries = append(r.Entries, ce)
		c := r.ByKind[e.Kind]
		if e.Scope.Scope == spec.ScopeOut {
			c.Excluded++
		} else {
			c.InScope++
			if impl {
				c.Implemented++
			}
		}
		r.ByKind[e.Kind] = c
	}
	for n := range idents {
		if !seen["$"+n] {
			r.Extra = append(r.Extra, "$"+n)
		}
	}
	for n := range cmds {
		if !seen["/"+n] {
			r.Extra = append(r.Extra, "/"+n)
		}
	}
	sort.Strings(r.Extra)
	return r
}

// lookup returns the registry key for an entry and whether it is implemented.
func lookup(e spec.Entry, idents, cmds, evs map[string]bool) (string, bool) {
	name := strings.ToLower(e.Name)
	if Language[e.Name] {
		return name, true
	}
	switch e.Kind {
	case spec.KindIdentifier:
		base := strings.TrimPrefix(name, "$")
		return "$" + base, idents[base]
	case spec.KindCommand:
		base := strings.TrimPrefix(name, "/")
		// Multi-word entries such as "/dcc send" are covered by their command.
		first, _, _ := strings.Cut(base, " ")
		return "/" + first, cmds[first]
	case spec.KindEvent:
		ev := strings.TrimPrefix(strings.TrimPrefix(name, "on "), "ctcp ")
		return "event:" + ev, evs[ev]
	}
	return name, false
}

func set(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[strings.ToLower(n)] = true
	}
	return m
}
