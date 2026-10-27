package script

import "strings"

type tokKind int

const (
	tWord tokKind = iota
	tSep          // end of line or a standalone |
	tLBrace
	tRBrace
)

type token struct {
	kind tokKind
	text string
	line int
	eol  bool // tSep that ends a line, as opposed to |
}

// ParseCommands parses a command line as typed in mIRC's editbox, where |
// separates commands and if/while/{ } work as in scripts.
func ParseCommands(text string) (Block, []error) {
	var errs []error
	b := parseBlock("", []line{{1, text}}, &errs)
	return b, errs
}

// parseBlock parses command lines into a statement tree.
func parseBlock(path string, lines []line, errs *[]error) Block {
	var toks []token
	for _, l := range lines {
		for _, w := range strings.Fields(l.text) {
			switch w {
			case "{":
				toks = append(toks, token{kind: tLBrace, line: l.n})
			case "}":
				toks = append(toks, token{kind: tRBrace, line: l.n})
			case "|":
				toks = append(toks, token{kind: tSep, line: l.n})
			default:
				toks = append(toks, token{kind: tWord, text: w, line: l.n})
			}
		}
		toks = append(toks, token{kind: tSep, line: l.n, eol: true})
	}
	p := &blockParser{path: path, toks: toks, errs: errs}
	return p.block(false)
}

type blockParser struct {
	path string
	toks []token
	pos  int
	errs *[]error
}

func (p *blockParser) errorf(n int, msg string) {
	*p.errs = append(*p.errs, &Error{Path: p.path, Line: n, Msg: msg})
}

func (p *blockParser) peek() (token, bool) {
	if p.pos >= len(p.toks) {
		return token{}, false
	}
	return p.toks[p.pos], true
}

func (p *blockParser) skipSeps() {
	for t, ok := p.peek(); ok && t.kind == tSep; t, ok = p.peek() {
		p.pos++
	}
}

// block parses statements up to a closing brace (when inner) or the end.
func (p *blockParser) block(inner bool) Block {
	var b Block
	for {
		p.skipSeps()
		t, ok := p.peek()
		if !ok {
			if inner {
				p.errorf(lastLine(p.toks), "missing }")
			}
			return b
		}
		if t.kind == tRBrace {
			p.pos++
			if inner {
				return b
			}
			p.errorf(t.line, "unmatched }")
			continue
		}
		if s := p.stmt(); s != nil {
			b = append(b, s)
		}
	}
}

func lastLine(toks []token) int {
	if len(toks) == 0 {
		return 0
	}
	return toks[len(toks)-1].line
}

// words consumes consecutive word tokens.
func (p *blockParser) words() ([]string, int) {
	var ws []string
	line := 0
	for t, ok := p.peek(); ok && t.kind == tWord; t, ok = p.peek() {
		if ws == nil {
			line = t.line
		}
		ws = append(ws, t.text)
		p.pos++
	}
	return ws, line
}

func (p *blockParser) stmt() Stmt {
	if t, _ := p.peek(); t.kind == tLBrace {
		p.pos++
		return &Nested{Body: p.block(true)}
	}
	ws, line := p.words()
	return p.stmtFrom(ws, line)
}

func (p *blockParser) stmtFrom(ws []string, line int) Stmt {
	if len(ws) == 0 {
		return nil
	}
	switch strings.ToLower(ws[0]) {
	case "if":
		cond, rest := p.splitCond(ws[1:])
		st := &If{Branches: []Branch{{Cond: cond, Body: p.body(rest, line), Line: line}}}
		p.chain(st)
		return st
	case "while":
		cond, rest := p.splitCond(ws[1:])
		return &While{Cond: cond, Body: p.body(rest, line), Line: line}
	case "elseif", "else":
		p.errorf(line, ws[0]+" without if")
		return nil
	}
	if len(ws) == 1 && len(ws[0]) > 1 && ws[0][0] == ':' {
		return &Label{Name: ws[0][1:], Line: line}
	}
	return &Cmd{Text: strings.Join(ws, " "), Line: line}
}

// chain attaches following elseif/else statements to an if.
func (p *blockParser) chain(st *If) {
	for {
		save := p.pos
		p.skipSeps()
		t, ok := p.peek()
		if !ok || t.kind != tWord {
			p.pos = save
			return
		}
		switch strings.ToLower(t.text) {
		case "elseif":
			ws, line := p.words()
			cond, rest := p.splitCond(ws[1:])
			st.Branches = append(st.Branches, Branch{Cond: cond, Body: p.body(rest, line), Line: line})
		case "else":
			ws, line := p.words()
			st.Else = p.body(ws[1:], line)
			st.HasElse = true
			return
		default:
			p.pos = save
			return
		}
	}
}

// body returns the body of an if/elseif/else/while: the rest of the line as
// a single statement, or a following { } block (which may start on the next line).
func (p *blockParser) body(rest []string, line int) Block {
	if len(rest) > 0 {
		if s := p.stmtFrom(rest, line); s != nil {
			return Block{s}
		}
		return nil
	}
	save := p.pos
	for t, ok := p.peek(); ok && t.kind == tSep && t.eol; t, ok = p.peek() {
		p.pos++
	}
	if t, ok := p.peek(); ok && t.kind == tLBrace {
		p.pos++
		return p.block(true)
	}
	p.pos = save
	return nil
}

// splitCond separates a condition from the command that follows it.
// Parenthesised conditions run to the matching close paren, continuing
// across && and ||. Unparenthesised ones take "v1 op v2" or a single value,
// or everything when a { follows.
func (p *blockParser) splitCond(ws []string) (string, []string) {
	if len(ws) == 0 {
		return "", nil
	}
	if strings.HasPrefix(strings.TrimLeft(ws[0], "!"), "(") {
		depth := 0
		for i, w := range ws {
			depth += strings.Count(w, "(") - strings.Count(w, ")")
			if depth > 0 || isJoiner(w) || (i+1 < len(ws) && isJoiner(ws[i+1])) {
				continue
			}
			return strings.Join(ws[:i+1], " "), ws[i+1:]
		}
		return strings.Join(ws, " "), nil
	}
	if t, ok := p.peek(); ok && t.kind == tLBrace {
		return strings.Join(ws, " "), nil
	}
	n := 1
	if len(ws) >= 3 && IsOperator(ws[1]) {
		n = 3
	}
	n = min(n, len(ws))
	return strings.Join(ws[:n], " "), ws[n:]
}

func isJoiner(w string) bool { return w == "&&" || w == "||" }

// Operators lists the comparison operators of if/while conditions.
var Operators = []string{
	"==", "===", "!=", "<", "<=", ">", ">=", "//", "\\\\", "&",
	"isin", "isincs", "iswm", "iswmcs", "isnum", "isletter", "isalnum",
	"isalpha", "islower", "isupper", "ison", "isop", "ishop", "isvoice",
	"isreg", "ischan", "isban", "isinvite", "isexcept", "isquiet", "isaop",
	"isavoice", "isignore", "isprotect", "isnotify",
}

var operatorSet = func() map[string]bool {
	m := make(map[string]bool, len(Operators))
	for _, o := range Operators {
		m[o] = true
	}
	return m
}()

// IsOperator reports whether w is a condition operator, optionally negated with !.
func IsOperator(w string) bool {
	w = strings.ToLower(w)
	if operatorSet[w] {
		return true
	}
	return len(w) > 1 && w[0] == '!' && w != "!=" && operatorSet[w[1:]]
}
