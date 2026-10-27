package interp

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// Frame is the state of one running alias or event body.
type Frame struct {
	RT     *Runtime
	Parent *Frame
	// File is the script the running code came from; nil at the command line.
	File  *script.File
	Alias *script.Alias

	Locals *Vars
	// Params are $1-. For identifier calls they are the comma-separated
	// arguments; for command calls the space-separated words.
	Params []string
	IsID   bool
	Prop   string
	// Show is false when the command was called with the . prefix.
	Show bool

	// Event holds the identifiers of the triggering event; aliases called
	// from an event see the same values.
	Event *Event
	// Cid is the connection id commands and identifiers act on ($cid).
	Cid int

	// Result is $result, set when a called alias returns.
	Result string
	// V1, V2 are $v1/$v2 from the last comparison.
	V1, V2 string
	// Err is $error, set when a script error is caught by :error.
	Err string
	// BigFloat is set by /bigfloat on.
	BigFloat bool
	// Caller is $caller: how this code was invoked (command, identifier,
	// event, timer, editbox, other).
	Caller string
	// Parms is $parms: the untokenized parameters.
	Parms string

	depth int
	// star is the current token while a line containing $* runs.
	star string
	// cmdLine is the line of the command being run, for error reports.
	cmdLine int
}

// Event carries an event's identifier values to the code it triggers.
type Event struct {
	// Name is the event name (TEXT, JOIN, 332, ...).
	Name string
	// Values are event identifiers by lower-case name without $: nick, chan, ...
	Values map[string]string
	// Conn is the connection the event came from, if any; its type belongs
	// to the irc package.
	Conn any
	// Halted is set by /halt or /haltdef to stop the default action.
	Halted bool
}

// Get returns an event identifier value.
func (e *Event) Get(name string) (string, bool) {
	if e == nil {
		return "", false
	}
	v, ok := e.Values[name]
	return v, ok
}

// NewFrame returns a top-level frame for code from file (nil for the command line).
func (rt *Runtime) NewFrame(file *script.File, ev *Event) *Frame {
	return &Frame{RT: rt, File: file, Locals: NewVars(), Show: true, Event: ev}
}

// child returns a frame for a called alias.
func (f *Frame) child(a *script.Alias) *Frame {
	return &Frame{
		RT: f.RT, Parent: f, File: a.File, Alias: a, Locals: NewVars(),
		Show: true, Event: f.Event, Cid: f.Cid, BigFloat: f.BigFloat, depth: f.depth + 1,
	}
}

// ScriptName returns $script: the running file's path.
func (f *Frame) ScriptName() string {
	if f.File == nil {
		return ""
	}
	return f.File.Path
}

// GetVar looks up a variable, local first then global.
func (f *Frame) GetVar(name string) (*Var, bool) {
	if v, ok := f.Locals.Get(name); ok {
		return v, true
	}
	return f.RT.Globals.Get(name)
}

// Param returns $N (1-based) or "".
func (f *Frame) Param(n int) string {
	if n < 1 || n > len(f.Params) {
		return ""
	}
	return f.Params[n-1]
}

// ParamRange returns $N- (to < 0) or $N-M joined by spaces.
func (f *Frame) ParamRange(from, to int) string {
	if from < 1 {
		from = 1
	}
	if to < 0 || to > len(f.Params) {
		to = len(f.Params)
	}
	if from > to {
		return ""
	}
	return strings.Join(f.Params[from-1:to], " ")
}

// Control flow is carried by these error values up the Go call stack.
type (
	returnSignal struct {
		value string
		has   bool
	}
	haltSignal     struct{}
	breakSignal    struct{}
	continueSignal struct{}
	gotoSignal     struct{ label string }
)

func (*returnSignal) Error() string  { return "/return outside alias" }
func (haltSignal) Error() string     { return "halted" }
func (breakSignal) Error() string    { return "/break outside loop" }
func (continueSignal) Error() string { return "/continue outside loop" }
func (g *gotoSignal) Error() string  { return fmt.Sprintf("/goto: '%s' not found", g.label) }

// ErrHalt stops all running script code (/halt, $$ with no value).
var ErrHalt error = haltSignal{}

// IsHalt reports whether err is a halt.
func IsHalt(err error) bool { return errors.As(err, new(haltSignal)) }

// ScriptError is a runtime error in a script, reported as
// "* /command: message (line N, file)".
type ScriptError struct {
	Cmd  string
	Msg  string
	Line int
	File string

	reported bool
}

func (e *ScriptError) Error() string {
	var b strings.Builder
	b.WriteString("* ")
	if e.Cmd != "" {
		b.WriteString("/" + e.Cmd + ": ")
	}
	b.WriteString(e.Msg)
	if e.File != "" {
		fmt.Fprintf(&b, " (line %d, %s)", e.Line, filepath.Base(e.File))
	}
	return b.String()
}

func asScriptError(err error) (*ScriptError, bool) {
	var se *ScriptError
	ok := errors.As(err, &se)
	return se, ok
}

// Errorf returns a script error for a command; the line and file are filled
// in by the executor.
func Errorf(cmd, format string, args ...any) error {
	return &ScriptError{Cmd: cmd, Msg: fmt.Sprintf(format, args...)}
}

// Call is an identifier invocation.
type Call struct {
	F    *Frame
	Name string
	// Args are the evaluated, comma-separated arguments.
	Args []string
	// Paren is set when the identifier was written with ( ).
	Paren bool
	Prop  string
}

// Arg returns argument n (0-based) or "".
func (c *Call) Arg(n int) string {
	if n < 0 || n >= len(c.Args) {
		return ""
	}
	return c.Args[n]
}

// Errorf returns an identifier error ("* Invalid parameters: $name").
func (c *Call) Errorf(format string, args ...any) error {
	return &ScriptError{Msg: fmt.Sprintf(format, args...)}
}

// InvalidParams is mIRC's standard identifier error.
func (c *Call) InvalidParams() error {
	return &ScriptError{Msg: "Invalid parameters: $" + c.Name}
}

// CmdCall is a command invocation.
type CmdCall struct {
	F    *Frame
	Name string
	// Args is the evaluated argument text (for Raw commands, unevaluated).
	Args string
	// Words are the argument words after evaluation brackets, unevaluated.
	Words []Word
	// Quiet is set by the . prefix.
	Quiet bool
}

// Fields returns the evaluated arguments split on spaces.
func (c *CmdCall) Fields() []string { return strings.Fields(c.Args) }

// Switches splits leading -switches from the arguments. It returns the
// switch letters (without -) and the remaining text.
func (c *CmdCall) Switches() (string, string) { return SplitSwitches(c.Args) }

// SplitSwitches splits a leading "-abc" word from text. A lone "-" is not a switch.
func SplitSwitches(text string) (string, string) {
	text = strings.TrimLeft(text, " ")
	if len(text) < 2 || text[0] != '-' || text[1] == ' ' {
		return "", text
	}
	word, rest, _ := strings.Cut(text, " ")
	return word[1:], strings.TrimLeft(rest, " ")
}

// Errorf returns an error attributed to this command.
func (c *CmdCall) Errorf(format string, args ...any) error {
	return Errorf(c.Name, format, args...)
}

// InvalidParams is mIRC's standard command error.
func (c *CmdCall) InvalidParams() error { return c.Errorf("invalid parameters") }
