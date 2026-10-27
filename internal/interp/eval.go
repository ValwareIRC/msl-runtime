package interp

import (
	"strconv"
	"strings"
)

// Word is one space-separated word of a command line after evaluation
// brackets have been resolved. Final words came wholly from a [ ] group:
// they are not evaluated again at the outer level.
type Word struct {
	Text  string
	Final bool

	glueL, glueR bool
}

// RawIdent is an identifier that receives its arguments unevaluated, such as
// $eval and $iif.
type RawIdent func(c *Call, raw []string) (string, error)

// RegisterRawIdent adds an identifier whose arguments are passed unevaluated.
func (rt *Runtime) RegisterRawIdent(name string, fn RawIdent) {
	if rt.rawIdents == nil {
		rt.rawIdents = map[string]RawIdent{}
	}
	rt.rawIdents[strings.ToLower(name)] = fn
}

// Eval evaluates text once: identifiers, variables, [ ] brackets and $+.
// Words are rejoined with single spaces and $null words are dropped, as in mIRC.
func (f *Frame) Eval(text string) (string, error) {
	ws, err := f.Brackets(SplitWords(text))
	if err != nil {
		return "", err
	}
	return f.EvalWords(ws)
}

// EvalWords evaluates bracket-resolved words at the outer level.
func (f *Frame) EvalWords(ws []Word) (string, error) { return f.evalWords(ws, false) }

func (f *Frame) evalWords(ws []Word, inBracket bool) (string, error) {
	var b strings.Builder
	glue, any := false, false
	for _, w := range ws {
		if !w.Final && w.Text == "$+" {
			glue = true
			continue
		}
		v := w.Text
		if inBracket || !w.Final {
			var err error
			if v, err = f.evalWord(w.Text); err != nil {
				return "", err
			}
		}
		if v == "" {
			continue
		}
		if any && !glue {
			b.WriteByte(' ')
		}
		b.WriteString(v)
		any, glue = true, false
	}
	return b.String(), nil
}

// SplitWords splits text on spaces, keeping an identifier's (...) argument
// list in one word even when it contains spaces.
func SplitWords(text string) []string {
	var out []string
	i := 0
	for i < len(text) {
		for i < len(text) && text[i] == ' ' {
			i++
		}
		if i >= len(text) {
			break
		}
		start := i
		if text[i] == '$' {
			i = skipIdentWord(text, i)
		}
		for i < len(text) && text[i] != ' ' {
			i++
		}
		out = append(out, text[start:i])
	}
	return out
}

// skipIdentWord returns the index just past an identifier's name and balanced
// argument list starting at i, or i if there is no argument list.
func skipIdentWord(text string, i int) int {
	j := i + 1
	for j < len(text) && (text[j] == '$' || text[j] == '~') {
		j++
	}
	for j < len(text) && isNameByte(text[j]) {
		j++
	}
	if j >= len(text) || text[j] != '(' {
		return i
	}
	if end := matchParen(text, j); end > 0 {
		return end + 1
	}
	return i
}

func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '+'
}

// matchParen returns the index of the ) matching the ( at open, or -1.
func matchParen(text string, open int) int {
	depth := 0
	for k := open; k < len(text); k++ {
		switch text[k] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return k
			}
		}
	}
	return -1
}

// SplitArgs splits an argument list on commas outside nested parentheses.
func SplitArgs(s string) []string {
	var out []string
	depth, start := 0, 0
	for k := 0; k < len(s); k++ {
		switch s[k] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:k])
				start = k + 1
			}
		}
	}
	return append(out, s[start:])
}

// Brackets resolves [ ] evaluation brackets, innermost first. A group's
// leading or trailing $+ glues its result to the neighbouring word.
func (f *Frame) Brackets(words []string) ([]Word, error) {
	stack := [][]Word{nil}
	for _, w := range words {
		switch w {
		case "[":
			stack = append(stack, nil)
			continue
		case "]":
			if len(stack) > 1 {
				group := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				res := Word{Final: true}
				if len(group) > 0 && !group[0].Final && group[0].Text == "$+" {
					res.glueL, group = true, group[1:]
				}
				if n := len(group); n > 0 && !group[n-1].Final && group[n-1].Text == "$+" {
					res.glueR, group = true, group[:n-1]
				}
				v, err := f.evalWords(group, true)
				if err != nil {
					return nil, err
				}
				res.Text = v
				top := len(stack) - 1
				stack[top] = append(stack[top], res)
				continue
			}
		}
		top := len(stack) - 1
		stack[top] = append(stack[top], Word{Text: w})
	}
	// Unclosed groups are kept as literal text.
	for len(stack) > 1 {
		group := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		top := len(stack) - 1
		stack[top] = append(append(stack[top], Word{Text: "["}), group...)
	}
	return glueWords(stack[0]), nil
}

// glueWords merges words marked for gluing by bracket $+.
func glueWords(ws []Word) []Word {
	out := ws[:0:0]
	for i := 0; i < len(ws); i++ {
		w := ws[i]
		if w.glueL && len(out) > 0 {
			prev := &out[len(out)-1]
			prev.Text += w.Text
			prev.Final = prev.Final && w.Final
			prev.glueR = w.glueR
			continue
		}
		if len(out) > 0 && out[len(out)-1].glueR {
			prev := &out[len(out)-1]
			prev.Text += w.Text
			prev.Final = prev.Final && w.Final
			prev.glueR = w.glueR
			continue
		}
		out = append(out, w)
	}
	for i := range out {
		out[i].glueL, out[i].glueR = false, false
	}
	return out
}

// SetMarkers installs a function that expands \-markers (such as $regsubex's
// \1 and \t) in words while fn runs. Words containing markers that are not
// identifiers or variables become the expanded text, unevaluated.
func (rt *Runtime) SetMarkers(expand func(word string) string, fn func() error) error {
	saved := rt.markers
	rt.markers = expand
	defer func() { rt.markers = saved }()
	return fn()
}

// evalWord evaluates one word.
func (f *Frame) evalWord(w string) (string, error) {
	if f.RT.markers != nil && strings.IndexByte(w, '\\') >= 0 && w[0] != '$' && w[0] != '%' {
		return f.RT.markers(w), nil
	}
	if len(w) < 2 {
		if w == "#" {
			return f.currentChannel(), nil
		}
		return w, nil
	}
	switch w[0] {
	case '%':
		if v, ok := f.GetVar(w); ok {
			return v.Value, nil
		}
		return "", nil
	case '#':
		if w[1] == '$' || w[1] == '%' {
			v, err := f.evalWord(w[1:])
			if err != nil || v == "" {
				return v, err
			}
			if strings.HasPrefix(v, "#") {
				return v, nil
			}
			return "#" + v, nil
		}
	case '$':
		return f.evalIdentWord(w)
	}
	return w, nil
}

func (f *Frame) currentChannel() string {
	if v, ok := f.Event.Get("chan"); ok {
		return v
	}
	return "#"
}

// evalIdentWord evaluates a word starting with $.
func (f *Frame) evalIdentWord(w string) (string, error) {
	i := 1
	required := false
	if w[i] == '$' {
		required = true
		i++
	}
	builtinOnly := false
	if i < len(w) && w[i] == '~' {
		builtinOnly = true
		i++
	}
	if i >= len(w) {
		return w, nil
	}

	// Positional parameters: $N, $N-, $N-M, with trailing text kept.
	if c := w[i]; c >= '0' && c <= '9' {
		v, rest := f.positional(w[i:])
		if required && v == "" {
			return "", ErrHalt
		}
		return v + rest, nil
	}

	switch w[i:] {
	case "*":
		return f.star, nil
	case "?", "!", "&":
		return "", nil
	}
	// $!name delays evaluation: it evaluates to the literal $name.
	if w[i] == '!' {
		return "$" + w[i+1:], nil
	}

	start := i
	for i < len(w) && isNameByte(w[i]) {
		i++
	}
	name := w[start:i]
	// Dotted alias names ($chat.add) win over $name.prop when such an
	// identifier exists.
	for i+1 < len(w) && w[i] == '.' && isNameByte(w[i+1]) {
		j := i + 1
		for j < len(w) && isNameByte(w[j]) {
			j++
		}
		candidate := name + "." + w[i+1:j]
		if !f.identExists(candidate) {
			break
		}
		name, i = candidate, j
	}
	paren := i < len(w) && w[i] == '('
	if name == "" && !paren {
		return w, nil
	}
	var argText string
	if paren {
		end := matchParen(w, i)
		if end < 0 {
			return w, nil
		}
		argText = w[i+1 : end]
		i = end + 1
	}
	var prop string
	if i+1 < len(w) && w[i] == '.' && isNameByte(w[i+1]) {
		j := i + 1
		for j < len(w) && isNameByte(w[j]) {
			j++
		}
		prop = w[i+1 : j]
		i = j
	}
	rest := w[i:]
	if rest != "" && !paren {
		// A bare identifier must be the whole word: "$nick," stays literal.
		return w, nil
	}

	v, err := f.callIdent(name, argText, paren, prop, builtinOnly)
	if err != nil {
		return "", err
	}
	if required && v == "" {
		return "", ErrHalt
	}
	return v + rest, nil
}

// positional evaluates $N, $N-, $N-M at the start of s and returns the
// value and the unconsumed rest.
func (f *Frame) positional(s string) (string, string) {
	j := 0
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	n, _ := strconv.Atoi(s[:j])
	if j < len(s) && s[j] == '-' {
		k := j + 1
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		to := -1
		if k > j+1 {
			to, _ = strconv.Atoi(s[j+1 : k])
		}
		return f.ParamRange(n, to), s[k:]
	}
	if n == 0 {
		return strconv.Itoa(len(f.Params)), s[j:]
	}
	return f.Param(n), s[j:]
}

// callIdent resolves and calls an identifier: raw built-ins, built-ins, then
// custom aliases.
func (f *Frame) callIdent(name, argText string, paren bool, prop string, builtinOnly bool) (string, error) {
	lname := strings.ToLower(name)
	var raw []string
	if paren {
		raw = SplitArgs(argText)
	}
	c := &Call{F: f, Name: lname, Paren: paren, Prop: prop}
	if fn, ok := f.RT.rawIdents[lname]; ok {
		return fn(c, raw)
	}
	for _, a := range raw {
		v, err := f.Eval(a)
		if err != nil {
			return "", err
		}
		c.Args = append(c.Args, v)
	}
	if fn, ok := f.RT.idents[lname]; ok {
		return fn(c)
	}
	if !builtinOnly {
		if a := f.RT.findAlias(f, name); a != nil {
			return f.call(a, c.Args, true, prop)
		}
	}
	return "", nil
}

// identExists reports whether name is a built-in identifier or a visible alias.
func (f *Frame) identExists(name string) bool {
	lname := strings.ToLower(name)
	if _, ok := f.RT.idents[lname]; ok {
		return true
	}
	if _, ok := f.RT.rawIdents[lname]; ok {
		return true
	}
	return f.RT.findAlias(f, name) != nil
}

// EvalN evaluates text n times, as $eval(text,n) does.
func (f *Frame) EvalN(text string, n int) (string, error) {
	for range n {
		v, err := f.Eval(text)
		if err != nil {
			return "", err
		}
		text = v
	}
	return text, nil
}
