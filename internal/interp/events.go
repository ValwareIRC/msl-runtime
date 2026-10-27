package interp

import (
	"strconv"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// Fire describes one occurrence of an event to dispatch to loaded scripts.
type Fire struct {
	// Kind is "on", "raw" or "ctcp".
	Kind string
	// Name is the event name (TEXT, JOIN, SOCKREAD, a raw numeric, ...).
	Name string
	// Params become $1-.
	Params []string
	// Event carries identifier values and the halted flag; it is created if nil.
	Event *Event
	// Cid is the connection the event belongs to (0 for none).
	Cid int
	// Address is the triggering user's nick!user@host, used for access levels.
	Address string
	// Me is set when the event was caused by the runtime's own user.
	Me bool
	// TargetAddress is the address of the user an event acts on (the opped
	// nick of on OP), for level comparisons such as on >=1:DEOP.
	TargetAddress string
	// IsOp reports whether the runtime's user is an op where the event
	// happened, for the @ prefix.
	IsOp func() bool
	// Match checks the event-specific fields of a definition (location,
	// match text, names). Nil matches everything.
	Match func(f *Frame, e *script.Event) (bool, error)
	// Only restricts dispatch to one script file (on LOAD, on UNLOAD).
	Only *script.File
	// First is dispatched before the other files (the /signal caller).
	First *script.File
}

// LevelSource returns the access levels of a user address and the users
// list mask that matched; the first level is the general level (possibly
// prefixed with =), the rest specific levels.
type LevelSource func(address string) (levels []string, mask string)

// SetLevels installs the users list lookup used for event access levels.
func (rt *Runtime) SetLevels(src LevelSource) { rt.levels = src }

// DefaultLevel is the level of users not in the users list (/dlevel).
func (rt *Runtime) DefaultLevel() string {
	if rt.defaultLevel == "" {
		return "1"
	}
	return rt.defaultLevel
}

// SetDefaultLevel changes the level of unlisted users.
func (rt *Runtime) SetDefaultLevel(l string) { rt.defaultLevel = l }

// handled records which event names scripts are dispatched for.
func (rt *Runtime) markHandled(name string) {
	if rt.handled == nil {
		rt.handled = map[string]bool{}
	}
	rt.handled[strings.ToUpper(name)] = true
}

// DeclareEvents records event names a feature package fires, for coverage.
func (rt *Runtime) DeclareEvents(names ...string) {
	for _, n := range names {
		rt.markHandled(n)
	}
}

// HandledEvents lists event names that can be fired.
func (rt *Runtime) HandledEvents() []string { return sortedKeys(rt.handled) }

// SetEventsEnabled turns event, ctcp and raw processing on or off (/remote).
func (rt *Runtime) SetEventsEnabled(kind string, on bool) {
	if rt.disabled == nil {
		rt.disabled = map[string]bool{}
	}
	rt.disabled[kind] = !on
}

// EventsEnabled reports whether a kind ("on", "ctcp", "raw") is processed.
func (rt *Runtime) EventsEnabled(kind string) bool { return !rt.disabled[kind] }

// Dispatch runs every matching event definition and reports whether any of
// them halted the default action. In each script file only the matching
// definition with the highest level triggers; ^ definitions run before the
// others.
func (rt *Runtime) Dispatch(fe Fire) bool {
	if fe.Event == nil {
		fe.Event = &Event{Name: fe.Name, Values: map[string]string{}}
	}
	if !rt.EventsEnabled(fe.Kind) {
		return false
	}
	levels, mask := rt.userLevels(fe.Address)
	fe.Event.Values["ulevel"] = strings.TrimPrefix(levels[0], "=")
	fe.Event.Values["maddress"] = mask
	for _, pre := range []bool{true, false} {
		for _, file := range rt.dispatchOrder(fe) {
			e, lvl := rt.best(file, fe, levels, pre)
			if e == nil {
				continue
			}
			rt.runEvent(file, e, fe, lvl)
		}
	}
	return fe.Event.Halted
}

func (rt *Runtime) userLevels(address string) ([]string, string) {
	if rt.levels != nil && address != "" {
		if ls, mask := rt.levels(address); len(ls) > 0 {
			return ls, mask
		}
	}
	return []string{rt.DefaultLevel()}, ""
}

// dispatchOrder returns the files an event is offered to.
func (rt *Runtime) dispatchOrder(fe Fire) []*script.File {
	if fe.Only != nil {
		return []*script.File{fe.Only}
	}
	if fe.First == nil {
		return rt.files
	}
	out := []*script.File{fe.First}
	for _, f := range rt.files {
		if f != fe.First {
			out = append(out, f)
		}
	}
	return out
}

// best returns the highest-level matching definition in file.
func (rt *Runtime) best(file *script.File, fe Fire, levels []string, pre bool) (*script.Event, string) {
	var best *script.Event
	bestRank := -1
	bestLevel := ""
	for _, e := range file.Events {
		if e.Kind != fe.Kind || (e.Group != nil && !e.Group.On) {
			continue
		}
		if fe.Kind != "ctcp" && !strings.EqualFold(e.Name, fe.Name) {
			continue
		}
		if strings.Contains(e.Prefixes, "^") != pre {
			continue
		}
		if !rt.prefixesAllow(e, fe) {
			continue
		}
		rank, ok := rt.accessibleFor(e, levels, fe)
		if !ok || rank <= bestRank {
			continue
		}
		if fe.Match != nil {
			f := rt.eventFrame(file, fe)
			ok, err := fe.Match(f, e)
			if err != nil {
				rt.report(err)
				continue
			}
			if !ok {
				continue
			}
		}
		best, bestRank, bestLevel = e, rank, e.Level
	}
	return best, bestLevel
}

func (rt *Runtime) prefixesAllow(e *script.Event, fe Fire) bool {
	if e.Me && !fe.Me {
		return false
	}
	if strings.Contains(e.Prefixes, "!") && fe.Me {
		return false
	}
	if strings.Contains(e.Prefixes, "&") && fe.Event.Halted {
		return false
	}
	if strings.Contains(e.Prefixes, "@") && (fe.IsOp == nil || !fe.IsOp()) {
		return false
	}
	return true
}

// levelComparators are the opper/opped level comparisons an event level may start with.
var levelComparators = []string{"<>", "<=", "=>", ">=", "<", ">", "="}

// accessibleFor applies accessible and, for levels such as >=1, compares the
// triggering user's general level with the target user's.
func (rt *Runtime) accessibleFor(e *script.Event, levels []string, fe Fire) (int, bool) {
	cmp, lvl := "", e.Level
	for _, p := range levelComparators {
		if strings.HasPrefix(lvl, p) && len(lvl) > len(p) && lvl[len(p)] >= '0' && lvl[len(p)] <= '9' {
			cmp, lvl = p, lvl[len(p):]
			break
		}
	}
	if cmp == "" {
		return accessible(e, levels)
	}
	plain := *e
	plain.Level = lvl
	rank, ok := accessible(&plain, levels)
	if !ok {
		return 0, false
	}
	target, _ := rt.userLevels(fe.TargetAddress)
	a, b := generalLevel(levels), generalLevel(target)
	switch cmp {
	case "<":
		ok = a < b
	case ">":
		ok = a > b
	case "<=":
		ok = a <= b
	case "=>", ">=":
		ok = a >= b
	case "<>":
		ok = a != b
	case "=":
		ok = a == b
	}
	return rank + 1, ok
}

func generalLevel(levels []string) int {
	if len(levels) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimPrefix(levels[0], "="))
	return n
}

// accessible reports whether a user with levels may trigger e, and the rank
// used to pick the highest matching definition.
func accessible(e *script.Event, levels []string) (int, bool) {
	lvl := e.Level
	if lvl == "*" {
		return 0, true
	}
	exact := strings.Contains(e.Prefixes, "+")
	n, err := strconv.Atoi(lvl)
	numeric := err == nil
	general := levels[0]
	specificOnly := strings.HasPrefix(general, "=")
	general = strings.TrimPrefix(general, "=")
	for i, l := range levels {
		l = strings.TrimPrefix(l, "=")
		if strings.EqualFold(l, lvl) && (i > 0 || specificOnly || exact || !numeric) {
			return rankOf(n, numeric), true
		}
	}
	if !numeric {
		return 0, false
	}
	g, err := strconv.Atoi(general)
	if err != nil || specificOnly {
		return 0, false
	}
	if exact {
		return n, g == n
	}
	return n, g >= n
}

func rankOf(n int, numeric bool) int {
	if numeric {
		return n
	}
	return 1
}

func (rt *Runtime) eventFrame(file *script.File, fe Fire) *Frame {
	f := rt.NewFrame(file, fe.Event)
	f.Params = fe.Params
	f.Cid = fe.Cid
	f.Caller = "event"
	return f
}

func (rt *Runtime) runEvent(file *script.File, e *script.Event, fe Fire, level string) {
	// "=" and "!" bodies only stop lower-level definitions from triggering.
	if len(e.Body) == 1 {
		if c, ok := e.Body[0].(*script.Cmd); ok && (c.Text == "=" || c.Text == "!") {
			return
		}
	}
	ev := fe.Event
	ev.Values["clevel"] = level
	ev.Values["event"] = strings.ToLower(eventIdentName(fe))
	ev.Values["matchkey"] = strings.Join(e.Fields, ":")
	f := rt.eventFrame(file, fe)
	_ = rt.Run(func() error {
		err := f.RunBody(e.Body)
		if IsHalt(err) {
			return nil
		}
		return err
	})
}

func eventIdentName(fe Fire) string {
	if fe.Kind == "ctcp" {
		return "ctcp"
	}
	return fe.Name
}
