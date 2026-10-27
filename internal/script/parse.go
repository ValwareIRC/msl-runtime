package script

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Kind selects how a file's top level is read.
type Kind int

const (
	// Remote files hold alias, on, raw, ctcp, menu and dialog definitions.
	Remote Kind = iota
	// Aliases files hold one "name commands" alias per definition, like mIRC's alias editor.
	Aliases
)

// Error is a parse problem at a line. mIRC skips bad definitions, so parsing
// continues past them.
type Error struct {
	Path string
	Line int
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s:%d: %s", e.Path, e.Line, e.Msg) }

const byteOrderMark = "\xef\xbb\xbf"

type line struct {
	n    int
	text string
}

// Parse reads a script file. It returns the file and any problems found;
// definitions with problems are skipped, the rest are kept.
func Parse(path, src string, kind Kind) (*File, []error) {
	f := &File{Path: path, Kind: kind, Groups: map[string]*Group{}}
	p := &fileParser{file: f, kind: kind, lines: preprocess(src)}
	p.run()
	return f, p.errs
}

type fileParser struct {
	file  *File
	kind  Kind
	lines []line
	i     int
	group *Group
	errs  []error
}

func (p *fileParser) errorf(n int, format string, args ...any) {
	p.errs = append(p.errs, &Error{Path: p.file.Path, Line: n, Msg: fmt.Sprintf(format, args...)})
}

func (p *fileParser) run() {
	for p.i < len(p.lines) {
		ln := p.lines[p.i]
		p.i++
		text := strings.TrimSpace(ln.text)
		if text == "" {
			continue
		}
		if p.groupDirective(ln.n, text) {
			continue
		}
		if p.kind == Aliases {
			p.aliasLine(ln.n, text, false)
			continue
		}
		word, rest := cutWord(text)
		switch strings.ToLower(word) {
		case "alias":
			p.aliasLine(ln.n, rest, true)
		case "on":
			p.eventLine(ln.n, "on", rest)
		case "raw":
			p.eventLine(ln.n, "raw", rest)
		case "ctcp":
			p.eventLine(ln.n, "ctcp", rest)
		case "menu":
			p.menuLine(ln.n, rest)
		case "dialog":
			p.dialogLine(ln.n, rest)
		default:
			p.errorf(ln.n, "unknown definition %q", word)
		}
	}
	if p.group != nil {
		p.errorf(len(p.lines), "group #%s has no #%s end", p.group.Name, p.group.Name)
	}
}

// groupDirective handles "#name on|off" and "#name end".
func (p *fileParser) groupDirective(n int, text string) bool {
	if !strings.HasPrefix(text, "#") {
		return false
	}
	name, state := cutWord(text)
	name = name[1:]
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "on", "off":
		if p.group != nil {
			p.errorf(n, "group #%s opened inside #%s", name, p.group.Name)
		}
		g := &Group{Name: name, On: strings.EqualFold(strings.TrimSpace(state), "on")}
		p.file.Groups[strings.ToLower(name)] = g
		p.file.GroupOrder = append(p.file.GroupOrder, g)
		p.group = g
	case "end":
		if p.group == nil || !strings.EqualFold(p.group.Name, name) {
			p.errorf(n, "#%s end without matching group", name)
		}
		p.group = nil
	default:
		return false
	}
	return true
}

// aliasLine reads "[-l] name commands" or "[-l] name { ... }".
func (p *fileParser) aliasLine(n int, text string, allowLocal bool) {
	a := &Alias{Group: p.group, File: p.file, Line: n}
	name, rest := cutWord(text)
	if allowLocal && strings.EqualFold(name, "-l") {
		a.Local = true
		name, rest = cutWord(rest)
	}
	a.Name = strings.TrimLeft(name, "/")
	if a.Name == "" {
		p.errorf(n, "alias without a name")
		return
	}
	body, ok := p.body(n, rest)
	if !ok {
		return
	}
	a.Body = body
	p.file.Aliases = append(p.file.Aliases, a)
}

// eventFields is how many colon-separated fields follow each on-event name
// before its commands. Events not listed take one field.
var eventFields = map[string]int{
	"AGENT": 0, "CONNECT": 0, "CONNECTFAIL": 0, "DISCONNECT": 0, "DNS": 0,
	"EXIT": 0, "LOAD": 0, "START": 0, "UNLOAD": 0, "MIDIEND": 0, "MP3END": 0,
	"WAVEEND": 0, "PLAYEND": 0, "NOSOUND": 0, "NICK": 0, "NOTIFY": 0,
	"UNOTIFY": 0, "PING": 0, "PONG": 0, "QUIT": 0, "USERMODE": 0,
	"TEXT": 2, "ACTION": 2, "NOTICE": 2, "OPEN": 2, "CLOSE": 2, "HOTLINK": 2,
	"KEYDOWN": 2, "KEYUP": 2, "CHAR": 2, "PARSELINE": 2,
	"DIALOG": 3,
}

// optionalMatchtext lists events whose match text field may be left out.
var optionalMatchtext = map[string]bool{"OPEN": true, "CLOSE": true}

// looksLikeCommands reports whether the text after an event's location
// field is its commands rather than a match text: it starts with { or /,
// or has no further colon.
func looksLikeCommands(rest string) bool {
	_, after, _ := strings.Cut(rest, ":")
	t := strings.TrimSpace(after)
	return strings.HasPrefix(t, "{") || strings.HasPrefix(t, "/") || !strings.Contains(after, ":")
}

// eventLine reads on/raw/ctcp definitions.
func (p *fileParser) eventLine(n int, kind, text string) {
	e := &Event{Kind: kind, Group: p.group, File: p.file, Line: n}
	head, rest, ok := cutField(text, false)
	if !ok {
		p.errorf(n, "%s definition without fields", kind)
		return
	}
	if kind == "on" || kind == "ctcp" {
		if strings.EqualFold(head, "me") {
			e.Me = true
			if head, rest, ok = cutField(rest, false); !ok {
				p.errorf(n, "%s definition without fields", kind)
				return
			}
		}
		e.Prefixes, e.Level = splitLevel(head)
	}

	var fields int
	switch kind {
	case "on":
		if head, rest, ok = cutField(rest, false); !ok {
			p.errorf(n, "on definition without an event name")
			return
		}
		e.Name = strings.ToUpper(head)
		fields = 1
		if c, known := eventFields[e.Name]; known {
			fields = c
		}
	case "raw":
		// Raw events have no access level; anyone can trigger them.
		e.Name, e.Level = head, "*"
		fields = 1
	case "ctcp":
		e.Name = "CTCP"
		fields = 2
	}
	regex := strings.Contains(e.Prefixes, "$")
	if optionalMatchtext[e.Name] && looksLikeCommands(rest) {
		fields = 1 // "on 1:CLOSE:@:commands" has no match text
	}
	for range fields {
		var f string
		if f, rest, ok = cutField(rest, regex); !ok {
			p.errorf(n, "%s %s: expected %d fields", kind, e.Name, fields)
			return
		}
		e.Fields = append(e.Fields, f)
	}
	body, ok := p.body(n, rest)
	if !ok {
		return
	}
	e.Body = body
	p.file.Events = append(p.file.Events, e)
}

// splitLevel separates prefix modifiers from the access level.
func splitLevel(s string) (prefixes, level string) {
	i := 0
	for i < len(s) && strings.IndexByte("!&^$@+", s[i]) >= 0 && i+1 < len(s) {
		i++
	}
	return s[:i], s[i:]
}

func (p *fileParser) menuLine(n int, text string) {
	targets, rest := cutWord(text)
	lines, ok := p.rawBody(n, rest)
	if !ok {
		return
	}
	m := &Menu{Targets: strings.Split(targets, ","), Lines: lines, Group: p.group, File: p.file, Line: n}
	p.file.Menus = append(p.file.Menus, m)
}

func (p *fileParser) dialogLine(n int, text string) {
	d := &Dialog{File: p.file, Line: n}
	name, rest := cutWord(text)
	if strings.EqualFold(name, "-l") {
		d.Local = true
		name, rest = cutWord(rest)
	}
	d.Name = name
	lines, ok := p.rawBody(n, rest)
	if !ok {
		return
	}
	d.Lines = lines
	p.file.Dialogs = append(p.file.Dialogs, d)
}

// body parses the commands of a definition: either a { } block starting on
// the header line or the rest of the header line.
func (p *fileParser) body(n int, rest string) (Block, bool) {
	rest = strings.TrimSpace(rest)
	if w, _ := cutWord(rest); w != "{" {
		return parseBlock(p.file.Path, []line{{n, rest}}, &p.errs), true
	}
	lines, ok := p.collect(n, rest)
	if !ok {
		return nil, false
	}
	return parseBlock(p.file.Path, lines, &p.errs), true
}

// rawBody returns the lines inside a { } block without parsing them.
func (p *fileParser) rawBody(n int, rest string) ([]string, bool) {
	lines, ok := p.collect(n, strings.TrimSpace(rest))
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if t := strings.TrimSpace(l.text); t != "" {
			out = append(out, t)
		}
	}
	return out, true
}

// collect gathers the lines between a header's opening brace and its match.
// first is the header text from the opening brace on.
func (p *fileParser) collect(n int, first string) ([]line, bool) {
	if w, _ := cutWord(first); w != "{" {
		p.errorf(n, "expected {")
		return nil, false
	}
	var out []line
	depth := 0
	cur := line{n, first}
	for {
		words := strings.Fields(cur.text)
		var kept []string
		for _, w := range words {
			if len(w) > 2 && strings.HasSuffix(w, ":{") {
				// A menu item's commands may start a block: "Item:{ ... }".
				depth++
				kept = append(kept, w)
				continue
			}
			switch w {
			case "{":
				depth++
				if depth == 1 {
					continue
				}
			case "}":
				depth--
				if depth == 0 {
					if len(kept) > 0 {
						out = append(out, line{cur.n, strings.Join(kept, " ")})
					}
					return out, true
				}
			}
			kept = append(kept, w)
		}
		out = append(out, line{cur.n, strings.Join(kept, " ")})
		if p.i >= len(p.lines) {
			p.errorf(n, "missing }")
			return nil, false
		}
		cur = p.lines[p.i]
		p.i++
	}
}

// preprocess normalises line endings, unwraps mIRC's ini storage format,
// removes comments and joins $& continuations.
func preprocess(src string) []line {
	src = strings.TrimPrefix(src, byteOrderMark)
	src = strings.ReplaceAll(src, "\r\n", "\n")
	raw := strings.Split(src, "\n")
	lines := make([]line, 0, len(raw))
	for i, t := range raw {
		lines = append(lines, line{i + 1, t})
	}
	lines = unwrapINI(lines)

	out := lines[:0:0]
	inComment := false
	for _, l := range lines {
		t := strings.TrimSpace(l.text)
		switch {
		case inComment:
			if strings.HasPrefix(t, "*/") || strings.HasSuffix(t, "*/") {
				inComment = false
			}
			continue
		case strings.HasPrefix(t, "/*"):
			inComment = !strings.HasSuffix(t, "*/") || t == "/*"
			continue
		case strings.HasPrefix(t, ";"):
			continue
		}
		if n := len(out); n > 0 && continues(out[n-1].text) {
			prev := strings.TrimSpace(out[n-1].text)
			out[n-1].text = strings.TrimSpace(strings.TrimSuffix(prev, "$&")) + " " + t
			continue
		}
		out = append(out, l)
	}
	return out
}

func continues(text string) bool {
	t := strings.TrimSpace(text)
	return t == "$&" || strings.HasSuffix(t, " $&")
}

// unwrapINI turns [script]/[aliases] sections of nN=text lines into plain lines.
func unwrapINI(lines []line) []line {
	first := -1
	for i, l := range lines {
		if t := strings.TrimSpace(l.text); t != "" {
			first = i
			break
		}
	}
	if first < 0 {
		return lines
	}
	sect := strings.ToLower(strings.TrimSpace(lines[first].text))
	if sect != "[script]" && sect != "[aliases]" {
		return lines
	}
	type numbered struct {
		idx int
		l   line
	}
	var items []numbered
	for _, l := range lines[first+1:] {
		key, val, ok := strings.Cut(l.text, "=")
		if !ok || len(key) < 2 || (key[0] != 'n' && key[0] != 'N') {
			continue
		}
		idx, err := strconv.Atoi(key[1:])
		if err != nil {
			continue
		}
		items = append(items, numbered{idx, line{l.n, val}})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].idx < items[j].idx })
	out := make([]line, len(items))
	for i, it := range items {
		out[i] = it.l
	}
	return out
}

// cutWord splits off the first space-separated word.
func cutWord(s string) (word, rest string) {
	s = strings.TrimLeft(s, " \t")
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimLeft(s[i:], " \t")
}

// cutField splits off one colon-separated field. With regex set, a field
// starting with / runs to its closing / (and flags), so colons inside a
// regular expression do not end it.
func cutField(s string, regex bool) (field, rest string, ok bool) {
	start := 0
	if regex && strings.HasPrefix(s, "/") {
		for j := 1; j < len(s); j++ {
			if s[j] == '\\' {
				j++
				continue
			}
			if s[j] == '/' {
				start = j + 1
				break
			}
		}
	}
	i := strings.IndexByte(s[start:], ':')
	if i < 0 {
		return "", "", false
	}
	return s[:start+i], s[start+i+1:], true
}
