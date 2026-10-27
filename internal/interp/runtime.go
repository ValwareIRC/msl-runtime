// Package interp runs mIRC scripts: it evaluates identifiers, variables and
// evaluation brackets, executes command bodies, and dispatches to built-in
// commands and identifiers registered by other packages.
//
// A Runtime is single-threaded, like mIRC: all script code runs on the
// goroutine that calls into it. Other goroutines (network readers, timers, UI)
// hand work to it through Post.
package interp

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// MaxDepth bounds alias call nesting.
const MaxDepth = 1000

// Output receives text the runtime would display.
type Output interface {
	// Echo shows a line in a window ("Status Window", "#chan", "@win", ...).
	Echo(window, text string)
	// Error reports a script error line.
	Error(text string)
}

// Ident is a built-in identifier.
type Ident func(c *Call) (string, error)

// Command is a built-in command.
type Command struct {
	Fn func(c *CmdCall) error
	// Raw commands receive their words after evaluation brackets but
	// otherwise unevaluated, for commands like /set whose first argument
	// names a variable.
	Raw bool
}

// Runtime is a script engine instance.
type Runtime struct {
	Out Output

	files     []*script.File
	aliases   map[string][]*script.Alias
	idents    map[string]Ident
	rawIdents map[string]RawIdent
	cmds      map[string]*Command
	operators map[string]Operator

	Globals *Vars

	// SendRaw, when set, sends an unknown command to the active server.
	SendRaw func(f *Frame, line string) error

	// ext holds state owned by other packages (connections, hash tables, ...).
	ext map[any]any

	posted chan func()

	// counters are /inc -c and -z variables, advanced by Tick.
	counters map[string]*counter
	// doneUnsets are -u0 variables, removed when the top-level run ends.
	doneUnsets []string
	running    int
	afterRun   []func()
	markers    func(string) string
	prefixCmds map[string]*Command

	// Exit, when set, ends the program (/exit); restart asks for a restart.
	Exit func(restart bool)

	levels       LevelSource
	defaultLevel string
	handled      map[string]bool
	disabled     map[string]bool
}

type counter struct {
	v      *Var
	by     float64
	toZero bool
}

// New returns an empty runtime that writes to out.
func New(out Output) *Runtime {
	rt := &Runtime{
		Out:      out,
		aliases:  map[string][]*script.Alias{},
		idents:   map[string]Ident{},
		cmds:     map[string]*Command{},
		Globals:  NewVars(),
		ext:      map[any]any{},
		posted:   make(chan func(), 1024),
		counters: map[string]*counter{},
	}
	rt.installLanguage()
	return rt
}

// Run executes fn as one top-level script run: -u0 variables set during it
// are removed when the outermost run finishes, and errors are reported.
func (rt *Runtime) Run(fn func() error) error {
	rt.running++
	err := fn()
	rt.running--
	if rt.running == 0 {
		for _, name := range rt.doneUnsets {
			rt.Globals.Unset(name)
		}
		rt.doneUnsets = rt.doneUnsets[:0]
		for _, fn := range rt.afterRun {
			fn()
		}
	}
	rt.report(err)
	return err
}

// AfterRun registers fn to run whenever an outermost script run finishes.
func (rt *Runtime) AfterRun(fn func()) { rt.afterRun = append(rt.afterRun, fn) }

// Running reports whether script code is currently executing.
func (rt *Runtime) Running() bool { return rt.running > 0 }

func (rt *Runtime) unsetWhenDone(f *Frame, name string) {
	if _, local := f.Locals.Get(name); local {
		return
	}
	rt.doneUnsets = append(rt.doneUnsets, name)
}

// Tick advances once-per-second work: -uN expiry and /inc -c, -z counters.
func (rt *Runtime) Tick(now time.Time) {
	rt.Globals.Expire(now)
	for k, c := range rt.counters {
		cur, ok := rt.Globals.Get(c.v.Name)
		if !ok || cur != c.v {
			delete(rt.counters, k)
			continue
		}
		n := Num(c.v.Value)
		if c.toZero {
			if n -= abs(c.by); n <= 0 {
				rt.Globals.Unset(c.v.Name)
				delete(rt.counters, k)
				continue
			}
		} else {
			n += c.by
		}
		c.v.Value = FormatNumber(n)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// RegisterIdent adds a built-in identifier, named without the $.
func (rt *Runtime) RegisterIdent(name string, fn Ident) {
	rt.idents[strings.ToLower(name)] = fn
}

// RegisterCommand adds a built-in command, named without the /.
func (rt *Runtime) RegisterCommand(name string, cmd Command) {
	c := cmd
	rt.cmds[strings.ToLower(name)] = &c
}

// RegisterPrefixCommand adds a command reached by any name starting with
// prefix, such as /timer1 and /timerfoo for /timer. The full name is in CmdCall.Name.
func (rt *Runtime) RegisterPrefixCommand(prefix string, cmd Command) {
	if rt.prefixCmds == nil {
		rt.prefixCmds = map[string]*Command{}
	}
	c := cmd
	rt.prefixCmds[strings.ToLower(prefix)] = &c
}

func (rt *Runtime) prefixCommand(name string) (*Command, bool) {
	for p, c := range rt.prefixCmds {
		if strings.HasPrefix(name, p) {
			return c, true
		}
	}
	return nil, false
}

// Idents lists registered identifier names, including raw ones, sorted.
func (rt *Runtime) Idents() []string {
	names := sortedKeys(rt.idents)
	for n := range rt.rawIdents {
		if n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// Commands lists registered command names, sorted.
func (rt *Runtime) Commands() []string { return sortedKeys(rt.cmds) }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Ext returns per-runtime state stored under key, creating it with mk.
func Ext[T any](rt *Runtime, key any, mk func() T) T {
	if v, ok := rt.ext[key]; ok {
		return v.(T)
	}
	v := mk()
	rt.ext[key] = v
	return v
}

// Files returns the loaded script files in load order.
func (rt *Runtime) Files() []*script.File { return rt.files }

// LoadFile parses a script file from disk and adds it.
func (rt *Runtime) LoadFile(path string, kind script.Kind) (*script.File, []error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []error{err}
	}
	return rt.LoadSource(path, string(data), kind)
}

// LoadSource parses script text and adds it. Parse problems are returned but
// do not prevent the rest of the file from loading.
func (rt *Runtime) LoadSource(path, src string, kind script.Kind) (*script.File, []error) {
	f, errs := script.Parse(path, src, kind)
	rt.files = append(rt.files, f)
	for _, a := range f.Aliases {
		k := strings.ToLower(a.Name)
		rt.aliases[k] = append(rt.aliases[k], a)
	}
	return f, errs
}

// Reindex rebuilds the alias lookup after files' aliases have been edited.
func (rt *Runtime) Reindex() {
	rt.aliases = map[string][]*script.Alias{}
	for _, f := range rt.files {
		for _, a := range f.Aliases {
			k := strings.ToLower(a.Name)
			rt.aliases[k] = append(rt.aliases[k], a)
		}
	}
}

// Unload removes a script file.
func (rt *Runtime) Unload(f *script.File) {
	for i, g := range rt.files {
		if g == f {
			rt.files = append(rt.files[:i], rt.files[i+1:]...)
			break
		}
	}
	for k, as := range rt.aliases {
		kept := as[:0]
		for _, a := range as {
			if a.File != f {
				kept = append(kept, a)
			}
		}
		if len(kept) == 0 {
			delete(rt.aliases, k)
		} else {
			rt.aliases[k] = kept
		}
	}
}

// findAlias returns the alias a frame would call by name: the first enabled
// definition in load order that is global or local to the frame's file.
func (rt *Runtime) findAlias(f *Frame, name string) *script.Alias {
	for _, a := range rt.aliases[strings.ToLower(name)] {
		if a.Group != nil && !a.Group.On {
			continue
		}
		if a.Local && (f == nil || a.File != f.File) {
			continue
		}
		return a
	}
	return nil
}

// HasAlias reports whether an alias is visible from f.
func (rt *Runtime) HasAlias(f *Frame, name string) bool { return rt.findAlias(f, name) != nil }

// Post queues fn to run on the runtime goroutine. It is safe to call from
// any goroutine.
func (rt *Runtime) Post(fn func()) { rt.posted <- fn }

// Posted is the queue Post feeds; the main loop drains it.
func (rt *Runtime) Posted() <-chan func() { return rt.posted }

// Exec runs a command line at the top level, as if from the command line
// with // (identifiers evaluated). It reports script errors through Out.
func (rt *Runtime) Exec(line string) error {
	f := rt.NewFrame(nil, nil)
	f.Caller = "editbox"
	block, errs := script.ParseCommands(line)
	if len(errs) > 0 {
		return rt.Run(func() error { return Errorf("", "%v", errs[0]) })
	}
	return rt.Run(func() error {
		err := f.RunBody(block)
		if IsHalt(err) {
			return nil
		}
		return err
	})
}

// CallAlias runs a named alias as a command with the given $1- parameters.
func (rt *Runtime) CallAlias(name string, params []string) error {
	f := rt.NewFrame(nil, nil)
	a := rt.findAlias(f, name)
	if a == nil {
		return fmt.Errorf("no such alias %q", name)
	}
	return rt.Run(func() error {
		_, err := f.call(a, params, false, "")
		return err
	})
}

// report shows a script error the way mIRC does in the status window.
func (rt *Runtime) report(err error) {
	if se, ok := asScriptError(err); ok && !se.reported {
		se.reported = true
		rt.Out.Error(se.Error())
	}
}
