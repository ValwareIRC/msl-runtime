// Package regex compiles mIRC's PCRE-style regular expressions, written as
// /pattern/flags or m/pattern/flags, or as a bare pattern.
package regex

import (
	"strings"
	"sync"

	"github.com/dlclark/regexp2"
)

// Regex is a compiled expression with the mIRC-specific flags.
type Regex struct {
	*regexp2.Regexp
	// Global is the g flag: match or substitute every occurrence.
	Global bool
	// Strip is the S flag: remove control codes before matching.
	Strip bool
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*Regex{}
)

// Compile parses an expression, reusing earlier compilations.
func Compile(expr string) (*Regex, error) {
	cacheMu.Lock()
	r, ok := cache[expr]
	cacheMu.Unlock()
	if ok {
		return r, nil
	}
	pattern, flags := Split(expr)
	var opts regexp2.RegexOptions
	r = &Regex{}
	for _, f := range flags {
		switch f {
		case 'i':
			opts |= regexp2.IgnoreCase
		case 'm':
			opts |= regexp2.Multiline
		case 's':
			opts |= regexp2.Singleline
		case 'x':
			opts |= regexp2.IgnorePatternWhitespace
		case 'g':
			r.Global = true
		case 'S':
			r.Strip = true
		case 'A':
			pattern = `\G(?:` + pattern + `)`
		case 'D':
			if !strings.HasSuffix(pattern, `\z`) && strings.HasSuffix(pattern, "$") {
				pattern = strings.TrimSuffix(pattern, "$") + `\z`
			}
		}
	}
	re, err := regexp2.Compile(pattern, opts)
	if err != nil {
		return nil, err
	}
	r.Regexp = re
	cacheMu.Lock()
	if len(cache) > 1024 {
		cache = map[string]*Regex{}
	}
	cache[expr] = r
	cacheMu.Unlock()
	return r, nil
}

// Split separates /pattern/flags (or m/pattern/flags, or mXpatternXflags
// with any delimiter X) into pattern and flags. A bare pattern has no flags.
func Split(expr string) (pattern, flags string) {
	s := expr
	delim := byte('/')
	switch {
	case strings.HasPrefix(s, "/"):
		s = s[1:]
	case len(s) > 2 && s[0] == 'm' && !isWord(s[1]):
		delim = s[1]
		s = s[2:]
	default:
		return expr, ""
	}
	end := strings.LastIndexByte(s, delim)
	if end < 0 {
		return expr, ""
	}
	return s[:end], s[end+1:]
}

func isWord(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// Match is one match: the whole text and its capture groups.
type Match struct {
	Text   string
	Index  int // rune offset, 0-based
	Groups []Group
}

// Group is one capture group of a match.
type Group struct {
	Text  string
	Index int // rune offset, 0-based; -1 when the group did not participate
	Name  string
}

// FindAll returns matches: every one with the g flag, else at most one.
func (r *Regex) FindAll(text string) ([]Match, error) {
	var out []Match
	m, err := r.FindStringMatch(text)
	for err == nil && m != nil {
		out = append(out, convert(r, m))
		if !r.Global {
			break
		}
		m, err = r.FindNextMatch(m)
	}
	return out, err
}

func convert(r *Regex, m *regexp2.Match) Match {
	res := Match{Text: m.String(), Index: m.Index}
	names := r.GetGroupNames()
	for i, g := range m.Groups() {
		if i == 0 {
			continue
		}
		grp := Group{Text: g.String(), Index: g.Index, Name: g.Name}
		if len(g.Captures) == 0 {
			grp.Index = -1
		}
		if i < len(names) && names[i] == g.Name && isNumber(g.Name) {
			grp.Name = ""
		}
		res.Groups = append(res.Groups, grp)
	}
	return res
}

func isNumber(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// Replace substitutes matches with repl, where \N and $N refer to groups.
// Only the first match is replaced unless the g flag is set.
func (r *Regex) Replace(text, repl string) (string, int, error) {
	count := 0
	out, err := r.ReplaceFunc(text, func(m regexp2.Match) string {
		count++
		return expand(repl, &m)
	}, -1, replaceLimit(r))
	return out, count, err
}

func replaceLimit(r *Regex) int {
	if r.Global {
		return -1
	}
	return 1
}

// expand applies \N and $N group references in a replacement.
func expand(repl string, m *regexp2.Match) string {
	var b strings.Builder
	groups := m.Groups()
	for i := 0; i < len(repl); i++ {
		c := repl[i]
		if (c == '\\' || c == '$') && i+1 < len(repl) && repl[i+1] >= '0' && repl[i+1] <= '9' {
			n := int(repl[i+1] - '0')
			i++
			if n < len(groups) {
				b.WriteString(groups[n].String())
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
