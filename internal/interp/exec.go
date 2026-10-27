package interp

import (
	"errors"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// ExecBlock runs statements in order. A /goto whose label is in this block
// continues after the label; otherwise it propagates outward.
func (f *Frame) ExecBlock(b script.Block) error {
	return f.execFrom(b, 0)
}

func (f *Frame) execFrom(b script.Block, start int) error {
	for i := start; i < len(b); i++ {
		err := f.execStmt(b[i])
		if err == nil {
			continue
		}
		var g *gotoSignal
		if errors.As(err, &g) {
			if j := findLabel(b, g.label); j >= 0 {
				i = j
				continue
			}
		}
		return err
	}
	return nil
}

func findLabel(b script.Block, name string) int {
	for i, s := range b {
		if l, ok := s.(*script.Label); ok && strings.EqualFold(l.Name, name) {
			return i
		}
	}
	return -1
}

func (f *Frame) execStmt(s script.Stmt) error {
	switch s := s.(type) {
	case *script.Cmd:
		return f.ExecLine(s.Text, s.Line)
	case *script.If:
		for _, br := range s.Branches {
			ok, err := f.Cond(br.Cond)
			if err != nil {
				return f.attribute(err, br.Line)
			}
			if ok {
				return f.ExecBlock(br.Body)
			}
		}
		if s.HasElse {
			return f.ExecBlock(s.Else)
		}
	case *script.While:
		for {
			ok, err := f.Cond(s.Cond)
			if err != nil {
				return f.attribute(err, s.Line)
			}
			if !ok {
				return nil
			}
			err = f.ExecBlock(s.Body)
			switch {
			case err == nil, errors.As(err, new(continueSignal)):
			case errors.As(err, new(breakSignal)):
				return nil
			default:
				return err
			}
		}
	case *script.Nested:
		return f.ExecBlock(s.Body)
	}
	return nil
}

// attribute fills in the line and file of a script error that lacks them.
func (f *Frame) attribute(err error, line int) error {
	if se, ok := asScriptError(err); ok && se.File == "" && f.File != nil {
		se.Line, se.File = line, f.File.Path
	}
	return err
}

// ExecLine evaluates and runs one command line.
func (f *Frame) ExecLine(text string, line int) error {
	f.cmdLine = line
	return f.attribute(f.execLine(text), line)
}

func (f *Frame) execLine(text string) error {
	text = strings.TrimSpace(text)
	quiet, noAlias := false, false
	for len(text) > 0 {
		switch text[0] {
		case '/':
			text = text[1:]
			continue
		case '.':
			quiet = true
			text = text[1:]
			continue
		case '!':
			if len(text) > 1 && text[1] != '=' {
				noAlias = true
				text = text[1:]
				continue
			}
		}
		break
	}
	if text == "" {
		return nil
	}
	words := SplitWords(text)
	if hasStar(words) {
		return f.forEachToken(words, quiet, noAlias)
	}
	ws, err := f.Brackets(words)
	if err != nil {
		return err
	}
	return f.dispatch(ws, quiet, noAlias)
}

func hasStar(words []string) bool {
	for _, w := range words {
		if w == "$*" {
			return true
		}
	}
	return false
}

// forEachToken runs a line containing $* once per $1- token.
func (f *Frame) forEachToken(words []string, quiet, noAlias bool) error {
	saved := f.star
	defer func() { f.star = saved }()
	for _, tok := range f.Params {
		f.star = tok
		ws, err := f.Brackets(words)
		if err != nil {
			return err
		}
		if err := f.dispatch(ws, quiet, noAlias); err != nil {
			return err
		}
	}
	return nil
}

// dispatch runs a bracket-resolved command line.
func (f *Frame) dispatch(ws []Word, quiet, noAlias bool) error {
	if len(ws) == 0 {
		return nil
	}
	head := ws[0]
	if len(ws) >= 2 && !head.Final && strings.HasPrefix(head.Text, "%") && ws[1].Text == "=" {
		return f.assign(head.Text, ws[2:])
	}
	name := head.Text
	args := ws[1:]
	if !head.Final && (strings.HasPrefix(name, "$") || strings.HasPrefix(name, "%")) {
		v, err := f.evalWord(name)
		if err != nil {
			return err
		}
		parts := strings.SplitN(strings.TrimSpace(v), " ", 2)
		name = parts[0]
		if len(parts) == 2 {
			args = append([]Word{{Text: parts[1], Final: true}}, args...)
		}
		if name == "" {
			return nil
		}
	}
	name = strings.TrimLeft(name, "/")
	lname := strings.ToLower(name)

	if !noAlias {
		if a := f.RT.findAlias(f, name); a != nil && !f.inCallStack(a) {
			text, err := f.EvalWords(args)
			if err != nil {
				return err
			}
			res, err := f.callQuiet(a, strings.Fields(text), quiet)
			if err == nil {
				f.Result = res
			}
			return err
		}
	}
	if cmd, ok := f.RT.cmds[lname]; ok {
		cc := &CmdCall{F: f, Name: lname, Words: args, Quiet: quiet}
		if cmd.Raw {
			cc.Args = rawText(args)
		} else {
			text, err := f.EvalWords(args)
			if err != nil {
				return err
			}
			cc.Args = text
		}
		return cmd.Fn(cc)
	}
	if cmd, ok := f.RT.prefixCommand(lname); ok {
		text, err := f.EvalWords(args)
		if err != nil {
			return err
		}
		return cmd.Fn(&CmdCall{F: f, Name: lname, Args: text, Words: args, Quiet: quiet})
	}
	text, err := f.EvalWords(args)
	if err != nil {
		return err
	}
	if f.RT.SendRaw != nil {
		return f.RT.SendRaw(f, strings.TrimSpace(name+" "+text))
	}
	return Errorf(lname, "not connected to server")
}

func rawText(ws []Word) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = w.Text
	}
	return strings.Join(parts, " ")
}

// inCallStack reports whether alias a is already running in this call chain;
// an alias called as a command cannot call itself.
func (f *Frame) inCallStack(a *script.Alias) bool {
	for fr := f; fr != nil; fr = fr.Parent {
		if fr.Alias == a {
			return true
		}
	}
	return false
}

// call runs alias a with $1- params and returns its /return value.
func (f *Frame) call(a *script.Alias, params []string, isid bool, prop string) (string, error) {
	return f.invoke(a, params, isid, prop, false)
}

func (f *Frame) callQuiet(a *script.Alias, params []string, quiet bool) (string, error) {
	return f.invoke(a, params, false, "", quiet)
}

func (f *Frame) invoke(a *script.Alias, params []string, isid bool, prop string, quiet bool) (string, error) {
	if f.depth >= MaxDepth {
		return "", Errorf(a.Name, "excessive recursion")
	}
	c := f.child(a)
	c.Params, c.IsID, c.Prop, c.Show = params, isid, prop, !quiet
	c.Caller, c.Parms = "command", strings.Join(params, " ")
	if isid {
		c.Caller, c.Parms = "identifier", strings.Join(params, ",")
	}
	err := c.RunBody(a.Body)
	var r *returnSignal
	if errors.As(err, &r) {
		return r.value, nil
	}
	return "", err
}

// RunBody runs an alias or event body, jumping to an :error label when a
// script error occurs. If the error is not cleared with /reseterror it keeps
// propagating to the caller afterwards.
func (f *Frame) RunBody(b script.Block) error {
	err := f.ExecBlock(b)
	var g *gotoSignal
	if errors.As(err, &g) {
		return f.attribute(Errorf("goto", "'%s' not found", g.label), f.cmdLine)
	}
	se, ok := asScriptError(err)
	if !ok {
		return err
	}
	at := findLabel(b, "error")
	if at < 0 {
		return err
	}
	f.Err = strings.TrimPrefix(se.Error(), "* ")
	if err := f.execFrom(b, at); err != nil {
		return err
	}
	if f.Err != "" {
		return se
	}
	return nil
}

// assign handles "%var = value", which performs a single math operation
// when the value is "N op N".
func (f *Frame) assign(name string, value []Word) error {
	v, err := f.EvalWords(value)
	if err != nil {
		return err
	}
	f.SetVar(name, SimpleMath(v), false)
	return nil
}

// SetVar sets a variable, updating a local of that name if one exists,
// otherwise a global (or a new local when local is set).
func (f *Frame) SetVar(name, value string, local bool) *Var {
	if v, ok := f.Locals.Get(name); ok {
		v.Value = value
		return v
	}
	if local {
		return f.Locals.Set(name, value)
	}
	return f.RT.Globals.Set(name, value)
}
