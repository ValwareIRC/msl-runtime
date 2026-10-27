package interp

import (
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// installLanguage registers the commands and identifiers that are part of
// the language itself rather than a feature area.
func (rt *Runtime) installLanguage() {
	rt.RegisterCommand("return", Command{Fn: cmdReturn})
	rt.RegisterCommand("halt", Command{Fn: cmdHalt})
	rt.RegisterCommand("haltdef", Command{Fn: cmdHaltdef})
	rt.RegisterCommand("break", Command{Fn: func(*CmdCall) error { return breakSignal{} }})
	rt.RegisterCommand("continue", Command{Fn: func(*CmdCall) error { return continueSignal{} }})
	rt.RegisterCommand("goto", Command{Fn: cmdGoto})
	rt.RegisterCommand("noop", Command{Fn: func(*CmdCall) error { return nil }})
	rt.RegisterCommand("reseterror", Command{Fn: func(c *CmdCall) error { c.F.Err = ""; return nil }})
	rt.RegisterCommand("set", Command{Fn: cmdSet, Raw: true})
	rt.RegisterCommand("var", Command{Fn: cmdVar, Raw: true})
	rt.RegisterCommand("unset", Command{Fn: cmdUnset, Raw: true})
	rt.RegisterCommand("unsetall", Command{Fn: func(c *CmdCall) error { c.F.RT.Globals.Clear(); return nil }})
	rt.RegisterCommand("inc", Command{Fn: func(c *CmdCall) error { return cmdIncDec(c, 1) }, Raw: true})
	rt.RegisterCommand("dec", Command{Fn: func(c *CmdCall) error { return cmdIncDec(c, -1) }, Raw: true})
	rt.RegisterCommand("tokenize", Command{Fn: cmdTokenize})
	rt.RegisterCommand("bigfloat", Command{Fn: cmdBigfloat})

	rt.RegisterIdent("null", func(*Call) (string, error) { return "", nil })
	rt.RegisterIdent("true", func(*Call) (string, error) { return "$true", nil })
	rt.RegisterIdent("false", func(*Call) (string, error) { return "$false", nil })
	rt.RegisterIdent("result", func(c *Call) (string, error) { return c.F.Result, nil })
	rt.RegisterIdent("v1", func(c *Call) (string, error) { return c.F.V1, nil })
	rt.RegisterIdent("v2", func(c *Call) (string, error) { return c.F.V2, nil })
	rt.RegisterIdent("prop", func(c *Call) (string, error) { return c.F.Prop, nil })
	rt.RegisterIdent("isid", func(c *Call) (string, error) { return Bool(c.F.IsID), nil })
	rt.RegisterIdent("show", func(c *Call) (string, error) { return Bool(c.F.Show), nil })
	rt.RegisterIdent("error", func(c *Call) (string, error) { return c.F.Err, nil })
	rt.RegisterIdent("script", identScript)
	rt.RegisterIdent("bigfloat", func(c *Call) (string, error) { return Bool(c.F.BigFloat), nil })
	rt.RegisterIdent("halted", func(c *Call) (string, error) {
		return Bool(c.F.Event != nil && c.F.Event.Halted), nil
	})
	rt.RegisterIdent("calc", func(c *Call) (string, error) {
		return FormatNumber(Calc(strings.Join(c.Args, ","))), nil
	})
	rt.RegisterIdent("+", func(c *Call) (string, error) { return strings.Join(c.Args, ""), nil })
	rt.RegisterIdent("var", identVar)
	rt.RegisterIdent("caller", func(c *Call) (string, error) {
		if c.F.Caller == "" {
			return "other", nil
		}
		return c.F.Caller, nil
	})
	rt.RegisterIdent("parms", func(c *Call) (string, error) {
		if c.F.Parms != "" {
			return c.F.Parms, nil
		}
		return strings.Join(c.F.Params, " "), nil
	})
	rt.RegisterIdent("eventparms", func(c *Call) (string, error) { return strings.Join(c.F.Params, " "), nil })
	rt.RegisterIdent("scriptline", func(c *Call) (string, error) { return strconv.Itoa(c.F.cmdLine), nil })
	rt.RegisterIdent("ifmatch", func(c *Call) (string, error) { return c.F.V1, nil })
	rt.RegisterIdent("unsafe", identUnsafe)
	rt.RegisterRawIdent("eval", identEval)
	rt.RegisterRawIdent("", identEval)
	rt.RegisterRawIdent("iif", identIif)
}

// Bool renders a Go bool as $true or $false.
func Bool(b bool) string {
	if b {
		return "$true"
	}
	return "$false"
}

func cmdReturn(c *CmdCall) error {
	return &returnSignal{value: c.Args, has: c.Args != ""}
}

func cmdHalt(c *CmdCall) error {
	if c.F.Event != nil {
		c.F.Event.Halted = true
	}
	return ErrHalt
}

func cmdHaltdef(c *CmdCall) error {
	if c.F.Event != nil {
		c.F.Event.Halted = true
	}
	return nil
}

func cmdGoto(c *CmdCall) error {
	label := strings.TrimSpace(c.Args)
	if label == "" {
		return c.InvalidParams()
	}
	return &gotoSignal{label: label}
}

func identScript(c *Call) (string, error) {
	if !c.Paren {
		return c.F.ScriptName(), nil
	}
	var files []*script.File
	for _, f := range c.F.RT.files {
		if f.Kind == script.Remote {
			files = append(files, f)
		}
	}
	if c.Arg(0) == "0" {
		return strconv.Itoa(len(files)), nil
	}
	if n := Int(c.Arg(0)); n > 0 {
		if n > len(files) {
			return "", nil
		}
		return files[n-1].Path, nil
	}
	for _, f := range files {
		if strings.EqualFold(f.Path, c.Arg(0)) || strings.HasSuffix(strings.ToLower(f.Path), strings.ToLower(c.Arg(0))) {
			return f.Path, nil
		}
	}
	return "", nil
}

func identEval(c *Call, raw []string) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	n := 1
	if len(raw) > 1 {
		v, err := c.F.Eval(raw[len(raw)-1])
		if err != nil {
			return "", err
		}
		n = Int(v)
	}
	text := raw[0]
	if len(raw) > 2 {
		text = strings.Join(raw[:len(raw)-1], ",")
	}
	if n <= 0 {
		return strings.TrimSpace(text), nil
	}
	return c.F.EvalN(text, n)
}

// identUnsafe returns text escaped so that one more evaluation yields it
// unchanged: $ and % become $chr() calls and [ ] brackets are neutralised.
func identUnsafe(c *Call) (string, error) {
	words := strings.Split(strings.Join(c.Args, ","), " ")
	for i, w := range words {
		switch w {
		case "[":
			words[i] = "$chr(91)"
			continue
		case "]":
			words[i] = "$chr(93)"
			continue
		}
		if strings.ContainsAny(w, "$%") {
			w = strings.NewReplacer("$", "$chr(36)", "%", "$chr(37)").Replace(w)
			if !strings.HasPrefix(w, "$chr(") {
				w = "$+(" + strings.ReplaceAll(w, "$chr(", ",$chr(") + ")"
			}
			words[i] = w
		}
	}
	return strings.Join(words, " "), nil
}

func identIif(c *Call, raw []string) (string, error) {
	if len(raw) < 2 {
		return "", c.InvalidParams()
	}
	ok, err := c.F.Cond(raw[0])
	if err != nil {
		return "", err
	}
	if ok {
		return c.F.Eval(raw[1])
	}
	if len(raw) > 2 {
		return c.F.Eval(raw[2])
	}
	return "", nil
}

func identVar(c *Call) (string, error) {
	pattern := c.Arg(0)
	if !strings.HasPrefix(pattern, "%") {
		pattern = "%" + pattern
	}
	locals := c.F.Locals.Match(pattern)
	all := append(locals, c.F.RT.Globals.Match(pattern)...)
	n := 1
	if len(c.Args) > 1 {
		n = Int(c.Arg(1))
	}
	if n == 0 {
		return strconv.Itoa(len(all)), nil
	}
	if n < 1 || n > len(all) {
		return "", nil
	}
	v := all[n-1]
	switch strings.ToLower(c.Prop) {
	case "value":
		return v.Value, nil
	case "local":
		return Bool(n <= len(locals)), nil
	case "secs":
		if v.UnsetAt.IsZero() {
			return "0", nil
		}
		return strconv.Itoa(int(time.Until(v.UnsetAt).Seconds() + 0.5)), nil
	}
	return v.Name, nil
}

// setOpts are the switches shared by /set, /var, /inc and /dec.
type setOpts struct {
	show, noMath, zero, onExit, global, local, keep, init, literal, perSec bool
	unset                                                                  int // -uN seconds; -1 when absent
}

func parseSetOpts(sw string) setOpts {
	o := setOpts{unset: -1}
	for i := 0; i < len(sw); i++ {
		switch sw[i] {
		case 's':
			o.show = true
		case 'n':
			o.noMath = true
		case 'z':
			o.zero = true
		case 'e':
			o.onExit = true
		case 'g':
			o.global = true
		case 'l':
			o.local = true
		case 'k':
			o.keep = true
		case 'i':
			o.init = true
		case 'p':
			o.literal = true
		case 'c':
			o.perSec = true
		case 'u':
			j := i + 1
			for j < len(sw) && sw[j] >= '0' && sw[j] <= '9' {
				j++
			}
			o.unset, _ = strconv.Atoi(sw[i+1 : j])
			i = j - 1
		}
	}
	return o
}

// leadingSwitches splits a non-final "-xyz" word off the front.
func leadingSwitches(ws []Word) (string, []Word) {
	if len(ws) > 0 && !ws[0].Final && len(ws[0].Text) > 1 && ws[0].Text[0] == '-' {
		return ws[0].Text[1:], ws[1:]
	}
	return "", ws
}

func cmdSet(c *CmdCall) error {
	sw, ws := leadingSwitches(c.Words)
	if len(ws) == 0 || !strings.HasPrefix(ws[0].Text, "%") {
		return c.InvalidParams()
	}
	return c.F.setWords(c, parseSetOpts(sw), ws[0].Text, ws[1:], c.F.Locals.has(ws[0].Text))
}

func (vs *Vars) has(name string) bool { _, ok := vs.Get(name); return ok }

// setWords is the shared assignment path of /set and /var.
func (f *Frame) setWords(c *CmdCall, o setOpts, name string, value []Word, local bool) error {
	if len(value) > 0 && !value[0].Final && value[0].Text == "=" {
		value = value[1:]
	}
	v, err := f.EvalWords(value)
	if err != nil {
		return err
	}
	if !o.noMath && !o.literal {
		v = SimpleMath(v)
	}
	if o.init {
		if _, exists := f.lookup(name, o); exists {
			return nil
		}
	}
	var target *Var
	switch {
	case o.global:
		target = f.RT.Globals.Set(name, v)
	case o.local || local:
		target = f.Locals.Set(name, v)
	default:
		target = f.SetVar(name, v, false)
	}
	f.applyTimers(target, o)
	if o.show && !c.Quiet {
		f.RT.Out.Echo(StatusWindow, "* Set "+target.Name+" to "+v)
	}
	return nil
}

func (f *Frame) lookup(name string, o setOpts) (*Var, bool) {
	switch {
	case o.global:
		return f.RT.Globals.Get(name)
	case o.local:
		return f.Locals.Get(name)
	}
	return f.GetVar(name)
}

func (f *Frame) applyTimers(v *Var, o setOpts) {
	if o.keep {
		return
	}
	v.UnsetAt = time.Time{}
	switch {
	case o.unset == 0:
		f.RT.unsetWhenDone(f, v.Name)
	case o.unset > 0:
		v.UnsetAt = time.Now().Add(time.Duration(o.unset) * time.Second)
	}
	if o.onExit {
		v.UnsetOnExit = true
	}
}

// cmdVar handles "/var [-switches] %a = 1, %b, %c = x".
func cmdVar(c *CmdCall) error {
	sw, ws := leadingSwitches(c.Words)
	o := parseSetOpts(sw)
	for _, decl := range splitVarDecls(ws) {
		if len(decl) == 0 || !strings.HasPrefix(decl[0].Text, "%") {
			return c.InvalidParams()
		}
		if err := c.F.setWords(c, o, decl[0].Text, decl[1:], !o.global); err != nil {
			return err
		}
	}
	return nil
}

// splitVarDecls splits /var words at commas that are followed by a %variable.
func splitVarDecls(ws []Word) [][]Word {
	var out [][]Word
	var cur []Word
	for i, w := range ws {
		if !w.Final && strings.HasSuffix(w.Text, ",") && i+1 < len(ws) && strings.HasPrefix(ws[i+1].Text, "%") {
			if t := strings.TrimSuffix(w.Text, ","); t != "" {
				cur = append(cur, Word{Text: t})
			}
			out = append(out, cur)
			cur = nil
			continue
		}
		cur = append(cur, w)
	}
	return append(out, cur)
}

func cmdUnset(c *CmdCall) error {
	sw, ws := leadingSwitches(c.Words)
	for _, w := range ws {
		name := w.Text
		if !strings.HasPrefix(name, "%") {
			continue
		}
		switch {
		case strings.Contains(sw, "l"):
			c.F.Locals.Unset(name)
		case strings.Contains(sw, "g"):
			c.F.RT.Globals.Unset(name)
		default:
			if c.F.Locals.Unset(name) == 0 || strings.ContainsAny(name, "*?") {
				c.F.RT.Globals.Unset(name)
			}
		}
	}
	if strings.Contains(sw, "s") && !c.Quiet {
		c.F.RT.Out.Echo(StatusWindow, "* Unset "+rawText(ws))
	}
	return nil
}

func cmdIncDec(c *CmdCall, sign float64) error {
	sw, ws := leadingSwitches(c.Words)
	if len(ws) == 0 || !strings.HasPrefix(ws[0].Text, "%") {
		return c.InvalidParams()
	}
	o := parseSetOpts(sw)
	by := 1.0
	if len(ws) > 1 {
		s, err := c.F.EvalWords(ws[1:])
		if err != nil {
			return err
		}
		by = Num(s)
	}
	name := ws[0].Text
	v, ok := c.F.GetVar(name)
	if !ok {
		v = c.F.SetVar(name, "0", false)
	}
	if o.perSec || o.zero {
		c.F.RT.counters[strings.ToLower(v.Name)] = &counter{v: v, by: by * sign, toZero: o.zero}
	} else {
		v.Value = FormatNumber(Num(v.Value) + by*sign)
	}
	c.F.applyTimers(v, o)
	if o.show && !c.Quiet {
		c.F.RT.Out.Echo(StatusWindow, "* Set "+v.Name+" to "+v.Value)
	}
	return nil
}

func cmdTokenize(c *CmdCall) error {
	sep, text, _ := strings.Cut(strings.TrimLeft(c.Args, " "), " ")
	c.F.Params = Tokens(text, rune(Int(sep)))
	return nil
}

// Tokens splits text on sep, dropping empty tokens as mIRC does.
func Tokens(text string, sep rune) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return r == sep })
}

func cmdBigfloat(c *CmdCall) error {
	switch strings.ToLower(strings.TrimSpace(c.Args)) {
	case "on":
		c.F.BigFloat = true
	case "off":
		c.F.BigFloat = false
	default:
		return c.InvalidParams()
	}
	return nil
}

// StatusWindow is the name echoes to the status window use.
const StatusWindow = "Status Window"
