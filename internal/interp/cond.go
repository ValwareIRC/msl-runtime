package interp

import (
	"strings"
	"unicode"

	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// Operator is a condition operator registered by another package, such as
// the channel operators ison and isop.
type Operator func(f *Frame, v1, v2 string) (bool, error)

// RegisterOperator adds a condition operator (lower case, without !).
func (rt *Runtime) RegisterOperator(name string, op Operator) {
	if rt.operators == nil {
		rt.operators = map[string]Operator{}
	}
	rt.operators[strings.ToLower(name)] = op
}

// Cond evaluates an if/while condition.
func (f *Frame) Cond(text string) (bool, error) {
	return f.condExpr(strings.TrimSpace(text))
}

// condExpr evaluates terms joined by && and ||, left to right with
// short-circuiting.
func (f *Frame) condExpr(s string) (bool, error) {
	terms, ops := splitJoiners(s)
	res, err := f.condTerm(terms[0])
	if err != nil {
		return false, err
	}
	for i, op := range ops {
		if (op == "&&" && !res) || (op == "||" && res) {
			continue
		}
		if res, err = f.condTerm(terms[i+1]); err != nil {
			return false, err
		}
	}
	return res, nil
}

// splitJoiners splits s at space-separated && and || outside parentheses.
func splitJoiners(s string) (terms, ops []string) {
	depth, start := 0, 0
	for k := 0; k < len(s); k++ {
		switch s[k] {
		case '(':
			depth++
		case ')':
			depth--
		case '&', '|':
			if depth == 0 && k+1 < len(s) && s[k+1] == s[k] &&
				(k == 0 || s[k-1] == ' ') && (k+2 == len(s) || s[k+2] == ' ') {
				terms = append(terms, strings.TrimSpace(s[start:k]))
				ops = append(ops, s[k:k+2])
				start = k + 2
				k++
			}
		}
	}
	return append(terms, strings.TrimSpace(s[start:])), ops
}

func (f *Frame) condTerm(s string) (bool, error) {
	neg := false
	if strings.HasPrefix(s, "!(") {
		neg, s = true, s[1:]
	}
	if strings.HasPrefix(s, "(") && matchParen(s, 0) == len(s)-1 {
		inner := strings.TrimSpace(s[1 : len(s)-1])
		res, err := f.condExpr(inner)
		return res != neg, err
	}
	if neg {
		s = "!" + s
	}
	return f.condAtom(s)
}

// condAtom evaluates "v1 [!]op v2" or a single value.
func (f *Frame) condAtom(s string) (bool, error) {
	words := SplitWords(s)
	for i := 1; i < len(words); i++ {
		if !f.isOperator(words[i]) {
			continue
		}
		v1, err := f.Eval(strings.Join(words[:i], " "))
		if err != nil {
			return false, err
		}
		v2, err := f.Eval(strings.Join(words[i+1:], " "))
		if err != nil {
			return false, err
		}
		f.V1, f.V2 = v1, v2
		op := strings.ToLower(words[i])
		neg := false
		if len(op) > 1 && op[0] == '!' && op != "!=" {
			neg, op = true, op[1:]
		}
		res, err := f.compare(op, v1, v2)
		return res != neg, err
	}
	neg := false
	if strings.HasPrefix(s, "!") && len(s) > 1 {
		neg, s = true, s[1:]
	}
	v, err := f.Eval(s)
	if err != nil {
		return false, err
	}
	f.V1, f.V2 = v, ""
	return truthy(v) != neg, nil
}

func (f *Frame) isOperator(w string) bool {
	if script.IsOperator(w) {
		return true
	}
	lw := strings.TrimPrefix(strings.ToLower(w), "!")
	_, ok := f.RT.operators[lw]
	return ok
}

func truthy(v string) bool {
	return v != "" && v != "0" && !strings.EqualFold(v, "$false")
}

func (f *Frame) compare(op, v1, v2 string) (bool, error) {
	n1, ok1 := ParseNumber(v1)
	n2, ok2 := ParseNumber(v2)
	both := ok1 && ok2
	switch op {
	case "==":
		if both {
			return n1 == n2, nil
		}
		return strings.EqualFold(v1, v2), nil
	case "===":
		return v1 == v2, nil
	case "!=":
		if both {
			return n1 != n2, nil
		}
		return !strings.EqualFold(v1, v2), nil
	case "<", "<=", ">", ">=":
		var c int
		if both {
			c = cmpFloat(n1, n2)
		} else {
			c = strings.Compare(strings.ToLower(v1), strings.ToLower(v2))
		}
		switch op {
		case "<":
			return c < 0, nil
		case "<=":
			return c <= 0, nil
		case ">":
			return c > 0, nil
		}
		return c >= 0, nil
	case "//", "\\\\":
		multiple := both && n1 != 0 && isWhole(n2/n1)
		return multiple == (op == "//"), nil
	case "&":
		return both && int64(n1)&int64(n2) != 0, nil
	case "isin":
		return strings.Contains(strings.ToLower(v2), strings.ToLower(v1)), nil
	case "isincs":
		return strings.Contains(v2, v1), nil
	case "iswm":
		return WildMatch(v1, v2, false), nil
	case "iswmcs":
		return WildMatch(v1, v2, true), nil
	case "isnum":
		return isNumInRange(v1, v2), nil
	case "isletter":
		if v2 == "" {
			r := []rune(v1)
			return len(r) == 1 && unicode.IsLetter(r[0]), nil
		}
		return v1 != "" && strings.Contains(v2, v1), nil
	case "isalnum":
		return allRunes(v1, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }), nil
	case "isalpha":
		return allRunes(v1, unicode.IsLetter), nil
	case "islower":
		return v1 != "" && v1 == strings.ToLower(v1), nil
	case "isupper":
		return v1 != "" && v1 == strings.ToUpper(v1), nil
	}
	if fn, ok := f.RT.operators[op]; ok {
		return fn(f, v1, v2)
	}
	return false, nil
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func allRunes(s string, ok func(rune) bool) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !ok(r) {
			return false
		}
	}
	return true
}

// isNumInRange implements "v1 isnum [n1-n2]".
func isNumInRange(v, rng string) bool {
	n, ok := ParseNumber(v)
	if !ok {
		return false
	}
	if rng == "" {
		return true
	}
	lo, hi, found := strings.Cut(rng, "-")
	if !found {
		m, ok := ParseNumber(rng)
		return ok && n == m
	}
	if l, ok := ParseNumber(lo); ok && n < l {
		return false
	}
	if h, ok := ParseNumber(hi); ok && n > h {
		return false
	}
	return true
}
