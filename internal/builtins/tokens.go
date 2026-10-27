package builtins

import (
	"sort"
	"strconv"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

func installTokens(rt *interp.Runtime) {
	ident(rt, identGettok, "gettok")
	ident(rt, identNumtok, "numtok")
	ident(rt, identAddtok, "addtok", "addtokcs")
	ident(rt, identDeltok, "deltok")
	ident(rt, identFindtok, "findtok", "findtokcs")
	ident(rt, identInstok, "instok")
	ident(rt, identIstok, "istok", "istokcs")
	ident(rt, identMatchtok, "matchtok", "matchtokcs")
	ident(rt, identPuttok, "puttok")
	ident(rt, identRemtok, "remtok", "remtokcs")
	ident(rt, identReptok, "reptok", "reptokcs")
	ident(rt, identSorttok, "sorttok", "sorttokcs")
	ident(rt, identWildtok, "wildtok", "wildtokcs")
}

// toks splits text on the character with code c, dropping empty tokens.
func toks(text, c string) ([]string, string) {
	sep := string(rune(interp.Int(c)))
	return interp.Tokens(text, rune(interp.Int(c))), sep
}

// index resolves a 1-based token number, negative counting from the end.
func index(n, count int) int {
	if n < 0 {
		return count + n + 1
	}
	return n
}

// tokRange parses N, N-, N-M (each may be negative) into 1-based bounds.
func tokRange(s string, count int) (from, to int) {
	s = strings.TrimSpace(s)
	k := strings.IndexByte(s[min(1, len(s)):], '-')
	if k < 0 {
		n := index(interp.Int(s), count)
		return n, n
	}
	k++
	from = index(interp.Int(s[:k]), count)
	if rest := s[k+1:]; rest != "" {
		return from, index(interp.Int(rest), count)
	}
	return from, count
}

func eqTok(name, a, b string) bool {
	if cs(name) {
		return a == b
	}
	return strings.EqualFold(a, b)
}

func identGettok(c *interp.Call) (string, error) {
	ts, sep := toks(c.Arg(0), c.Arg(2))
	if strings.TrimSpace(c.Arg(1)) == "0" {
		return strconv.Itoa(len(ts)), nil
	}
	from, to := tokRange(c.Arg(1), len(ts))
	from, to = max(from, 1), min(to, len(ts))
	if from > to {
		return "", nil
	}
	return strings.Join(ts[from-1:to], sep), nil
}

func identNumtok(c *interp.Call) (string, error) {
	ts, _ := toks(c.Arg(0), c.Arg(1))
	return strconv.Itoa(len(ts)), nil
}

func identAddtok(c *interp.Call) (string, error) {
	ts, sep := toks(c.Arg(0), c.Arg(2))
	for _, t := range ts {
		if eqTok(c.Name, t, c.Arg(1)) {
			return strings.Join(ts, sep), nil
		}
	}
	return strings.Join(append(ts, c.Arg(1)), sep), nil
}

func identDeltok(c *interp.Call) (string, error) {
	ts, sep := toks(c.Arg(0), c.Arg(2))
	from, to := tokRange(c.Arg(1), len(ts))
	from, to = max(from, 1), min(to, len(ts))
	if from > to {
		return strings.Join(ts, sep), nil
	}
	return strings.Join(append(ts[:from-1:from-1], ts[to:]...), sep), nil
}

// nthArgs reads the optional N of (text,token,N,C) identifiers, which also
// accept the older (text,token,C) form meaning N = 1.
func nthArgs(c *interp.Call, nIdx int) (n int, sepArg string) {
	if len(c.Args) > nIdx+1 {
		return interp.Int(c.Arg(nIdx)), c.Arg(nIdx + 1)
	}
	return 1, c.Arg(nIdx)
}

func identFindtok(c *interp.Call) (string, error) {
	n, sepArg := nthArgs(c, 2)
	ts, _ := toks(c.Arg(0), sepArg)
	return nthMatch(ts, n, func(t string) bool { return eqTok(c.Name, t, c.Arg(1)) }, func(i int) string {
		return strconv.Itoa(i + 1)
	}), nil
}

// nthMatch returns the Nth matching token rendered by out, or the match
// count when n is zero.
func nthMatch(ts []string, n int, match func(string) bool, out func(int) string) string {
	count := 0
	for i, t := range ts {
		if match(t) {
			count++
			if count == n {
				return out(i)
			}
		}
	}
	if n == 0 {
		return strconv.Itoa(count)
	}
	return ""
}

func identInstok(c *interp.Call) (string, error) {
	ts, sep := toks(c.Arg(0), c.Arg(3))
	n := index(interp.Int(c.Arg(2)), len(ts))
	n = clamp(n, 1, len(ts)+1)
	out := append([]string(nil), ts[:n-1]...)
	out = append(out, c.Arg(1))
	return strings.Join(append(out, ts[n-1:]...), sep), nil
}

func identIstok(c *interp.Call) (string, error) {
	ts, _ := toks(c.Arg(0), c.Arg(2))
	for _, t := range ts {
		if eqTok(c.Name, t, c.Arg(1)) {
			return interp.Bool(true), nil
		}
	}
	return interp.Bool(false), nil
}

func identMatchtok(c *interp.Call) (string, error) {
	n, sepArg := nthArgs(c, 2)
	ts, _ := toks(c.Arg(0), sepArg)
	sub := fold(c.Name, strings.TrimSpace(c.Arg(1)))
	return nthMatch(ts, n, func(t string) bool { return strings.Contains(fold(c.Name, t), sub) },
		func(i int) string { return ts[i] }), nil
}

func identWildtok(c *interp.Call) (string, error) {
	n, sepArg := nthArgs(c, 2)
	ts, _ := toks(c.Arg(0), sepArg)
	pattern := strings.TrimSpace(c.Arg(1))
	return nthMatch(ts, n, func(t string) bool { return interp.WildMatch(pattern, t, cs(c.Name)) },
		func(i int) string { return ts[i] }), nil
}

func identPuttok(c *interp.Call) (string, error) {
	ts, sep := toks(c.Arg(0), c.Arg(3))
	n := index(interp.Int(c.Arg(2)), len(ts))
	if n >= 1 && n <= len(ts) {
		ts[n-1] = c.Arg(1)
	}
	return strings.Join(ts, sep), nil
}

func identRemtok(c *interp.Call) (string, error) {
	n, sepArg := nthArgs(c, 2)
	ts, sep := toks(c.Arg(0), sepArg)
	return strings.Join(replaceNth(ts, n, func(t string) bool { return eqTok(c.Name, t, c.Arg(1)) }, nil), sep), nil
}

func identReptok(c *interp.Call) (string, error) {
	n, sepArg := nthArgs(c, 3)
	ts, sep := toks(c.Arg(0), sepArg)
	repl := c.Arg(2)
	return strings.Join(replaceNth(ts, n, func(t string) bool { return eqTok(c.Name, t, c.Arg(1)) }, &repl), sep), nil
}

// replaceNth removes (repl nil) or replaces the Nth matching token, or all
// matching tokens when n is zero.
func replaceNth(ts []string, n int, match func(string) bool, repl *string) []string {
	out := make([]string, 0, len(ts))
	count := 0
	for _, t := range ts {
		if match(t) {
			count++
			if n == 0 || count == n {
				if repl != nil {
					out = append(out, *repl)
				}
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

// identSorttok sorts tokens: alphabetic by default; n numeric, c channel
// nick prefix order, a alphanumeric, r reversed.
func identSorttok(c *interp.Call) (string, error) {
	ts, sep := toks(c.Arg(0), c.Arg(1))
	opts := c.Arg(2)
	key := func(s string) string {
		if cs(c.Name) {
			return s
		}
		return strings.ToLower(s)
	}
	var less func(a, b string) bool
	switch {
	case strings.Contains(opts, "n"):
		less = func(a, b string) bool { return interp.Num(a) < interp.Num(b) }
	case strings.Contains(opts, "c"):
		less = func(a, b string) bool {
			ra, rb := prefixRank(a), prefixRank(b)
			if ra != rb {
				return ra < rb
			}
			return key(strings.TrimLeft(a, nickPrefixes)) < key(strings.TrimLeft(b, nickPrefixes))
		}
	case strings.Contains(opts, "a"):
		less = func(a, b string) bool { return naturalLess(key(a), key(b)) }
	default:
		less = func(a, b string) bool { return key(a) < key(b) }
	}
	sort.SliceStable(ts, func(i, j int) bool { return less(ts[i], ts[j]) })
	if strings.Contains(opts, "r") {
		for i, j := 0, len(ts)-1; i < j; i, j = i+1, j-1 {
			ts[i], ts[j] = ts[j], ts[i]
		}
	}
	return strings.Join(ts, sep), nil
}

// nickPrefixes are channel status prefixes from highest to lowest.
const nickPrefixes = "~&@%+"

func prefixRank(nick string) int {
	if nick == "" {
		return len(nickPrefixes)
	}
	if i := strings.IndexByte(nickPrefixes, nick[0]); i >= 0 {
		return i
	}
	return len(nickPrefixes)
}
