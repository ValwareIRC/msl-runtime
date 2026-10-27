package data

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ValwareIRC/msl-runtime/internal/builtins"
	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/regex"
)

// maxRegml is how many named $regex results are remembered.
const maxRegml = 50

// regmlItem is one remembered capture.
type regmlItem struct {
	text    string
	pos     int // 1-based character position
	bytePos int // 1-based UTF-8 byte position
	group   int
	match   int
}

func (d *Data) installRegex() {
	rt := d.rt
	rt.RegisterIdent("regex", d.identRegex)
	rt.RegisterIdent("regml", d.identRegml)
	rt.RegisterIdent("regmlex", d.identRegmlex)
	rt.RegisterRawIdent("regsub", d.identRegsub)
	rt.RegisterRawIdent("regsubex", d.identRegsubex)
	rt.RegisterIdent("regerrstr", func(*interp.Call) (string, error) { return d.regErr, nil })
}

// splitName separates the optional leading name argument.
func splitName(args []string, n int) (string, []string) {
	if len(args) > n {
		return args[0], args[1:]
	}
	return "default", args
}

// storeMatches remembers captures under a name. Without the F flag,
// captures are numbered in the order found and empty ones are skipped;
// with F they keep their group numbers. A pattern without groups stores
// each whole match.
func (d *Data) storeMatches(name string, re *regex.Regex, ms []regex.Match, groupNumbers bool) {
	var items []regmlItem
	for mi, m := range ms {
		if len(m.Groups) == 0 {
			items = append(items, regmlItem{text: m.Text, pos: m.Index + 1, group: 0, match: mi + 1})
			continue
		}
		for gi, g := range m.Groups {
			if !groupNumbers && (g.Index < 0 || g.Text == "") {
				continue
			}
			items = append(items, regmlItem{text: g.Text, pos: g.Index + 1, group: gi + 1, match: mi + 1})
		}
	}
	key := strings.ToLower(name)
	d.regml[key] = items
	for i, k := range d.regmlQ {
		if k == key {
			d.regmlQ = append(d.regmlQ[:i], d.regmlQ[i+1:]...)
			break
		}
	}
	d.regmlQ = append(d.regmlQ, key)
	if len(d.regmlQ) > maxRegml {
		delete(d.regml, d.regmlQ[0])
		d.regmlQ = d.regmlQ[1:]
	}
}

func hasFlag(expr string, flag byte) bool {
	_, flags := regex.Split(expr)
	return strings.IndexByte(flags, flag) >= 0
}

func (d *Data) compile(expr string) (*regex.Regex, bool) {
	re, err := regex.Compile(expr)
	if err != nil {
		d.regErr = err.Error()
		return nil, false
	}
	d.regErr = ""
	return re, true
}

// identRegex implements $regex([name], text, pattern).
func (d *Data) identRegex(c *interp.Call) (string, error) {
	name, args := splitName(c.Args, 2)
	if len(args) < 2 {
		return "", c.InvalidParams()
	}
	re, ok := d.compile(args[1])
	if !ok {
		return "", nil
	}
	text := args[0]
	if re.Strip {
		text = builtins.Strip(text, "")
	}
	ms, err := re.FindAll(text)
	if err != nil {
		d.regErr = err.Error()
		return "0", nil
	}
	d.storeMatches(name, re, ms, hasFlag(args[1], 'F'))
	for i := range d.regml[strings.ToLower(name)] {
		it := &d.regml[strings.ToLower(name)][i]
		it.bytePos = len(string([]rune(text)[:max(it.pos-1, 0)])) + 1
	}
	return strconv.Itoa(len(ms)), nil
}

func (d *Data) regmlItems(c *interp.Call, n int) ([]regmlItem, []string) {
	name, args := splitName(c.Args, n)
	if len(args) < n && name != "default" {
		args = c.Args
		name = "default"
	}
	return d.regml[strings.ToLower(name)], args
}

// identRegml implements $regml([name], N, [&binvar]).pos|bytepos|group|match.
func (d *Data) identRegml(c *interp.Call) (string, error) {
	var items []regmlItem
	var args []string
	switch {
	case len(c.Args) >= 2 && !strings.HasPrefix(c.Arg(1), "&"):
		items, args = d.regml[strings.ToLower(c.Arg(0))], c.Args[1:]
	default:
		items, args = d.regml["default"], c.Args
	}
	return d.regmlResult(c, items, args)
}

func (d *Data) regmlResult(c *interp.Call, items []regmlItem, args []string) (string, error) {
	n := interp.Int(args[0])
	if n == 0 {
		return strconv.Itoa(len(items)), nil
	}
	if n < 1 || n > len(items) {
		return "", nil
	}
	return d.regmlProp(c, items[n-1], args[1:])
}

func (d *Data) regmlProp(c *interp.Call, it regmlItem, rest []string) (string, error) {
	switch strings.ToLower(c.Prop) {
	case "pos":
		return strconv.Itoa(it.pos), nil
	case "bytepos":
		return strconv.Itoa(it.bytePos), nil
	case "group":
		return strconv.Itoa(it.group), nil
	case "match":
		return strconv.Itoa(it.match), nil
	}
	if len(rest) > 0 && strings.HasPrefix(rest[0], "&") {
		d.setBin(rest[0], []byte(it.text))
		return strconv.Itoa(len(it.text)), nil
	}
	return it.text, nil
}

// identRegmlex implements $regmlex([name], M, N, [&binvar]).
func (d *Data) identRegmlex(c *interp.Call) (string, error) {
	items, args := d.regml["default"], c.Args
	if len(c.Args) >= 2 {
		if _, ok := interp.ParseNumber(c.Arg(0)); !ok {
			items, args = d.regml[strings.ToLower(c.Arg(0))], c.Args[1:]
		}
	}
	m := interp.Int(args[0])
	group := 1
	if len(args) > 1 && !strings.HasPrefix(args[1], "&") {
		group = interp.Int(args[1])
		args = args[1:]
	}
	if m == 0 {
		count := 0
		for _, it := range items {
			count = max(count, it.match)
		}
		return strconv.Itoa(count), nil
	}
	var inMatch []regmlItem
	for _, it := range items {
		if it.match == m {
			inMatch = append(inMatch, it)
		}
	}
	if group == 0 {
		return strconv.Itoa(len(inMatch)), nil
	}
	for _, it := range inMatch {
		if it.group == group || (it.group == 0 && group == 1) {
			return d.regmlProp(c, it, args[1:])
		}
	}
	return "", nil
}

// assignResult stores a substitution result in a %variable or &binvar.
func (d *Data) assignResult(c *interp.Call, target, value string) {
	switch {
	case strings.HasPrefix(target, "&"):
		d.setBin(target, []byte(value))
	case strings.HasPrefix(target, "%"):
		c.F.SetVar(target, value, false)
	}
}

// identRegsub implements $regsub([name], text, pattern, subtext, %var|&binvar).
// The target is a variable name, so it is not evaluated.
func (d *Data) identRegsub(c *interp.Call, raw []string) (string, error) {
	if len(raw) < 4 {
		return "", c.InvalidParams()
	}
	target := strings.TrimSpace(raw[len(raw)-1])
	args := make([]string, 0, len(raw)-1)
	for _, a := range raw[:len(raw)-1] {
		v, err := c.F.Eval(a)
		if err != nil {
			return "", err
		}
		args = append(args, v)
	}
	name := "default"
	if len(args) == 4 {
		name, args = args[0], args[1:]
	}
	re, ok := d.compile(args[1])
	if !ok {
		return "", nil
	}
	text := args[0]
	if re.Strip {
		text = builtins.Strip(text, "")
	}
	ms, _ := re.FindAll(text)
	d.storeMatches(name, re, ms, hasFlag(args[1], 'F'))
	out, n, err := re.Replace(text, args[2])
	if err != nil {
		d.regErr = err.Error()
		return "0", nil
	}
	d.assignResult(c, target, out)
	return strconv.Itoa(n), nil
}

// identRegsubex implements $regsubex([name], text, pattern, subtext,
// [%var|&binvar]). The subtext is evaluated for each match, with markers
// \n (match number), \t (match text), \a and \A (all captures, spaced or
// not), \0 (capture count) and \N (capture N). Marker text is inserted
// as-is and never evaluated.
func (d *Data) identRegsubex(c *interp.Call, raw []string) (string, error) {
	var name, target string
	switch len(raw) {
	case 3:
		name = "default"
	case 4:
		name, raw = raw[0], raw[1:]
	case 5:
		name, target, raw = raw[0], strings.TrimSpace(raw[4]), raw[1:4]
	default:
		return "", c.InvalidParams()
	}
	var err error
	if name, err = c.F.Eval(name); err != nil {
		return "", err
	}
	text, err := c.F.Eval(raw[0])
	if err != nil {
		return "", err
	}
	pattern, err := c.F.Eval(raw[1])
	if err != nil {
		return "", err
	}
	re, ok := d.compile(pattern)
	if !ok {
		return "", nil
	}
	if re.Strip {
		text = builtins.Strip(text, "")
	}
	ms, err := re.FindAll(text)
	if err != nil {
		d.regErr = err.Error()
		return "", nil
	}
	d.storeMatches(name, re, ms, hasFlag(pattern, 'F'))

	runes := []rune(text)
	var b strings.Builder
	last := 0
	for i, m := range ms {
		var v string
		expand := func(word string) string { return expandMarkers(word, i+1, m) }
		err := c.F.RT.SetMarkers(expand, func() (err error) {
			v, err = c.F.Eval(raw[2])
			return err
		})
		if err != nil {
			return "", err
		}
		b.WriteString(string(runes[last:m.Index]))
		b.WriteString(v)
		last = m.Index + utf8.RuneCountInString(m.Text)
	}
	b.WriteString(string(runes[min(last, len(runes)):]))
	if target != "" {
		d.assignResult(c, target, b.String())
		return strconv.Itoa(len(ms)), nil
	}
	return b.String(), nil
}

// expandMarkers replaces $regsubex markers in a word with the values for
// match number num.
func expandMarkers(word string, num int, m regex.Match) string {
	var b strings.Builder
	for i := 0; i < len(word); i++ {
		if word[i] != '\\' || i+1 >= len(word) {
			b.WriteByte(word[i])
			continue
		}
		j := i + 1
		switch word[j] {
		case 'n':
			b.WriteString(strconv.Itoa(num))
		case 't':
			b.WriteString(m.Text)
		case 'a', 'A':
			parts := make([]string, 0, len(m.Groups))
			for _, g := range m.Groups {
				parts = append(parts, g.Text)
			}
			sep := " "
			if word[j] == 'A' {
				sep = ""
			}
			b.WriteString(strings.Join(parts, sep))
		default:
			k := j
			for k < len(word) && word[k] >= '0' && word[k] <= '9' {
				k++
			}
			if k == j {
				b.WriteByte(word[i])
				continue
			}
			n, _ := strconv.Atoi(word[j:k])
			switch {
			case n == 0:
				b.WriteString(strconv.Itoa(len(m.Groups)))
			case n <= len(m.Groups):
				b.WriteString(m.Groups[n-1].Text)
			case len(m.Groups) == 0 && n == 1:
				b.WriteString(m.Text)
			}
			i = k - 1
			continue
		}
		i = j
	}
	return b.String()
}
