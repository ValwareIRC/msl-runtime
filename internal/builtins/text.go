package builtins

import (
	"crypto/rand"
	"math/big"
	mrand "math/rand/v2"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

func installText(rt *interp.Runtime) {
	ident(rt, identLen, "len")
	ident(rt, func(c *interp.Call) (string, error) { return strings.ToUpper(c.Arg(0)), nil }, "upper")
	ident(rt, func(c *interp.Call) (string, error) { return strings.ToLower(c.Arg(0)), nil }, "lower")
	ident(rt, identLeft, "left")
	ident(rt, identRight, "right")
	ident(rt, identMid, "mid")
	ident(rt, identPos, "pos", "poscs")
	ident(rt, identCount, "count", "countcs")
	ident(rt, identStr, "str")
	ident(rt, identRemove, "remove", "removecs")
	ident(rt, identReplace, "replace", "replacecs")
	ident(rt, identReplacex, "replacex", "replacexcs")
	ident(rt, identAsc, "asc")
	ident(rt, identChr, "chr")
	ident(rt, func(c *interp.Call) (string, error) { return "\r", nil }, "cr")
	ident(rt, func(c *interp.Call) (string, error) { return "\n", nil }, "lf")
	ident(rt, func(c *interp.Call) (string, error) { return "\r\n", nil }, "crlf")
	ident(rt, func(c *interp.Call) (string, error) { return "\t", nil }, "tab")
	ident(rt, identQt, "qt")
	ident(rt, identNoqt, "noqt")
	ident(rt, identStrip, "strip")
	ident(rt, identIsLower, "islower")
	ident(rt, identIsUpper, "isupper")
	ident(rt, identUTFEncode, "utfencode")
	ident(rt, identUTFDecode, "utfdecode")
	ident(rt, identRand, "rand")
	ident(rt, identRands, "rands")
	ident(rt, identOrd, "ord")
}

func runes(s string) []rune { return []rune(s) }

func identLen(c *interp.Call) (string, error) {
	return strconv.Itoa(utf8.RuneCountInString(c.Arg(0))), nil
}

func identLeft(c *interp.Call) (string, error) {
	r := runes(c.Arg(0))
	n := interp.Int(c.Arg(1))
	if n < 0 {
		n = len(r) + n
	}
	return string(r[:clamp(n, 0, len(r))]), nil
}

func identRight(c *interp.Call) (string, error) {
	r := runes(c.Arg(0))
	n := interp.Int(c.Arg(1))
	if n < 0 {
		n = len(r) + n
	}
	n = clamp(n, 0, len(r))
	return string(r[len(r)-n:]), nil
}

// identMid implements $mid(text,S,N): S counts from 1, or from the end when
// negative; N of zero or absent means to the end, negative means all but N.
func identMid(c *interp.Call) (string, error) {
	r := runes(c.Arg(0))
	s := interp.Int(c.Arg(1))
	if s < 0 {
		s = len(r) + s + 1
	}
	if s < 1 {
		s = 1
	}
	if s > len(r) {
		return "", nil
	}
	start := s - 1
	end := len(r)
	if len(c.Args) > 2 {
		switch n := interp.Int(c.Arg(2)); {
		case n > 0:
			end = min(start+n, len(r))
		case n < 0:
			end = len(r) + n
		}
	}
	if end <= start {
		return "", nil
	}
	return string(r[start:end]), nil
}

func clamp(n, lo, hi int) int { return max(lo, min(hi, n)) }

// fold lowers text unless the identifier is a case-sensitive variant.
func fold(name, s string) string {
	if cs(name) {
		return s
	}
	return strings.ToLower(s)
}

func identPos(c *interp.Call) (string, error) {
	text, sub := fold(c.Name, c.Arg(0)), fold(c.Name, c.Arg(1))
	if sub == "" {
		return "", nil
	}
	n := 1
	if len(c.Args) > 2 {
		n = interp.Int(c.Arg(2))
	}
	if n == 0 {
		return strconv.Itoa(strings.Count(text, sub)), nil
	}
	off := 0
	for i := 1; ; i++ {
		k := strings.Index(text[off:], sub)
		if k < 0 {
			return "", nil
		}
		if i == n {
			return strconv.Itoa(utf8.RuneCountInString(text[:off+k]) + 1), nil
		}
		off += k + len(sub)
	}
}

func identCount(c *interp.Call) (string, error) {
	text := fold(c.Name, c.Arg(0))
	total := 0
	for _, sub := range c.Args[min(1, len(c.Args)):] {
		if sub = fold(c.Name, sub); sub != "" {
			total += strings.Count(text, sub)
		}
	}
	return strconv.Itoa(total), nil
}

func identStr(c *interp.Call) (string, error) {
	n := interp.Int(c.Arg(1))
	if n <= 0 {
		return "", nil
	}
	return strings.Repeat(c.Arg(0), n), nil
}

// replaceFold replaces every occurrence of old in s, matching case-insensitively.
func replaceFold(s, old, new string) string {
	if old == "" {
		return s
	}
	lower, lold := strings.ToLower(s), strings.ToLower(old)
	if len(lower) != len(s) {
		// Case folding changed byte lengths; fall back to rune-wise matching.
		return replaceFoldRunes(s, old, new)
	}
	var b strings.Builder
	for {
		k := strings.Index(lower, lold)
		if k < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:k])
		b.WriteString(new)
		s, lower = s[k+len(old):], lower[k+len(lold):]
	}
}

func replaceFoldRunes(s, old, new string) string {
	rs, ro := []rune(s), []rune(old)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if i+len(ro) <= len(rs) && strings.EqualFold(string(rs[i:i+len(ro)]), old) {
			b.WriteString(new)
			i += len(ro)
			continue
		}
		b.WriteRune(rs[i])
		i++
	}
	return b.String()
}

func replaceAll(name, s, old, new string) string {
	if cs(name) {
		if old == "" {
			return s
		}
		return strings.ReplaceAll(s, old, new)
	}
	return replaceFold(s, old, new)
}

func identRemove(c *interp.Call) (string, error) {
	s := c.Arg(0)
	for _, sub := range c.Args[min(1, len(c.Args)):] {
		s = replaceAll(c.Name, s, sub, "")
	}
	return s, nil
}

func identReplace(c *interp.Call) (string, error) {
	s := c.Arg(0)
	for i := 1; i+1 < len(c.Args); i += 2 {
		s = replaceAll(c.Name, s, c.Args[i], c.Args[i+1])
	}
	return s, nil
}

// identReplacex replaces all pairs in one pass, so replaced text is never
// matched again.
func identReplacex(c *interp.Call) (string, error) {
	s := c.Arg(0)
	type pair struct{ old, new string }
	var pairs []pair
	for i := 1; i+1 < len(c.Args); i += 2 {
		if c.Args[i] != "" {
			pairs = append(pairs, pair{c.Args[i], c.Args[i+1]})
		}
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		matched := false
		for _, p := range pairs {
			if i+len(p.old) <= len(s) && (s[i:i+len(p.old)] == p.old || !cs(c.Name) && strings.EqualFold(s[i:i+len(p.old)], p.old)) {
				b.WriteString(p.new)
				i += len(p.old)
				matched = true
				break
			}
		}
		if !matched {
			_, n := utf8.DecodeRuneInString(s[i:])
			b.WriteString(s[i : i+n])
			i += n
		}
	}
	return b.String(), nil
}

func identAsc(c *interp.Call) (string, error) {
	r, _ := utf8.DecodeRuneInString(c.Arg(0))
	if c.Arg(0) == "" {
		return "", nil
	}
	return strconv.Itoa(int(r)), nil
}

func identChr(c *interp.Call) (string, error) {
	n := interp.Int(c.Arg(0))
	if n < 0 || n > unicode.MaxRune {
		return "", nil
	}
	if n == 0 {
		return "", nil
	}
	return string(rune(n)), nil
}

func identQt(c *interp.Call) (string, error) {
	s := c.Arg(0)
	if !strings.HasPrefix(s, `"`) {
		s = `"` + s
	}
	if !strings.HasSuffix(s, `"`) || len(s) == 1 {
		s += `"`
	}
	return s, nil
}

func identNoqt(c *interp.Call) (string, error) {
	s := c.Arg(0)
	s = strings.TrimPrefix(s, `"`)
	return strings.TrimSuffix(s, `"`), nil
}

// Control codes used in IRC text formatting.
const (
	ctrlBold      = '\x02'
	ctrlColor     = '\x03'
	ctrlHexColor  = '\x04'
	ctrlPlain     = '\x0f'
	ctrlReverse   = '\x16'
	ctrlItalic    = '\x1d'
	ctrlStrike    = '\x1e'
	ctrlUnderline = '\x1f'
)

// stripOption maps a control code to its $strip option letter.
var stripOption = map[byte]byte{
	ctrlBold: 'b', ctrlUnderline: 'u', ctrlReverse: 'r', ctrlItalic: 'i', ctrlStrike: 'e',
}

// Strip removes formatting control codes. opts selects which, from
// b (bold), u (underline), r (reverse), i (italic), e (strikethrough),
// c (colour); empty means all. The plain code is always removed.
func Strip(s, opts string) string {
	all := opts == "" || strings.ContainsAny(opts, "mo") && !strings.ContainsAny(opts, "buriec")
	want := func(o byte) bool { return all || strings.IndexByte(opts, o) >= 0 }
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; ch {
		case ctrlBold, ctrlUnderline, ctrlReverse, ctrlItalic, ctrlStrike:
			if want(stripOption[ch]) {
				continue
			}
		case ctrlPlain:
			continue
		case ctrlColor:
			if want('c') {
				i = skipColor(s, i)
				continue
			}
		case ctrlHexColor:
			if want('c') {
				i = skipHexColor(s, i)
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// skipColor returns the index of the last byte of a \x03[NN[,NN]] code.
func skipColor(s string, i int) int {
	j := i + 1
	j = skipDigits(s, j, 2)
	if j > i+1 && j+1 < len(s) && s[j] == ',' && isDigit(s[j+1]) {
		j = skipDigits(s, j+1, 2)
	}
	return j - 1
}

func skipHexColor(s string, i int) int {
	j := i + 1
	if isHex6(s, j) {
		j += 6
		if j+6 < len(s)+1 && j < len(s) && s[j] == ',' && isHex6(s, j+1) {
			j += 7
		}
	}
	return j - 1
}

func isHex6(s string, j int) bool {
	if j+6 > len(s) {
		return false
	}
	for k := j; k < j+6; k++ {
		if !strings.ContainsRune("0123456789abcdefABCDEF", rune(s[k])) {
			return false
		}
	}
	return true
}

func skipDigits(s string, j, maxN int) int {
	for n := 0; n < maxN && j < len(s) && isDigit(s[j]); n++ {
		j++
	}
	return j
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func identStrip(c *interp.Call) (string, error) { return Strip(c.Arg(0), c.Arg(1)), nil }

func identIsLower(c *interp.Call) (string, error) {
	return interp.Bool(c.Arg(0) == strings.ToLower(c.Arg(0))), nil
}

func identIsUpper(c *interp.Call) (string, error) {
	return interp.Bool(c.Arg(0) == strings.ToUpper(c.Arg(0))), nil
}

// identUTFEncode returns text's UTF-8 bytes, one character per byte.
func identUTFEncode(c *interp.Call) (string, error) {
	var b strings.Builder
	for _, by := range []byte(c.Arg(0)) {
		b.WriteRune(rune(by))
	}
	return b.String(), nil
}

// identUTFDecode reverses $utfencode: characters below 256 are taken as
// UTF-8 bytes and decoded; invalid sequences are returned unchanged.
func identUTFDecode(c *interp.Call) (string, error) {
	in := c.Arg(0)
	bs := make([]byte, 0, len(in))
	for _, r := range in {
		if r > 0xff {
			return in, nil
		}
		bs = append(bs, byte(r))
	}
	if !utf8.Valid(bs) {
		return in, nil
	}
	return string(bs), nil
}

func identRand(c *interp.Call) (string, error)  { return randBetween(c, false) }
func identRands(c *interp.Call) (string, error) { return randBetween(c, true) }

func randBetween(c *interp.Call, secure bool) (string, error) {
	a, b := c.Arg(0), c.Arg(1)
	pick := func(n int64) int64 {
		if n <= 0 {
			return 0
		}
		if secure {
			v, err := rand.Int(rand.Reader, big.NewInt(n))
			if err != nil {
				return 0
			}
			return v.Int64()
		}
		return mrand.Int64N(n)
	}
	ra, _ := utf8.DecodeRuneInString(a)
	rb, _ := utf8.DecodeRuneInString(b)
	if utf8.RuneCountInString(a) == 1 && utf8.RuneCountInString(b) == 1 && unicode.IsLetter(ra) && unicode.IsLetter(rb) {
		if ra > rb {
			ra, rb = rb, ra
		}
		return string(ra + rune(pick(int64(rb-ra)+1))), nil
	}
	lo, hi := int64(interp.Num(a)), int64(interp.Num(b))
	if lo > hi {
		lo, hi = hi, lo
	}
	return strconv.FormatInt(lo+pick(hi-lo+1), 10), nil
}

func identOrd(c *interp.Call) (string, error) {
	s := c.Arg(0)
	n := interp.Int(s)
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return s + suffix, nil
}
