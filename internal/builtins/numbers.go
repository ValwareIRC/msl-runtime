package builtins

import (
	"math"
	"math/big"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

func installNumbers(rt *interp.Runtime) {
	unary := map[string]func(float64) float64{
		"abs": math.Abs, "ceil": math.Ceil, "floor": math.Floor, "int": math.Trunc,
		"sqrt": math.Sqrt, "cbrt": math.Cbrt, "log": math.Log, "log2": math.Log2, "log10": math.Log10,
	}
	for name, fn := range unary {
		ident(rt, func(c *interp.Call) (string, error) { return num(fn(interp.Num(c.Arg(0)))) }, name)
	}
	trig := map[string]func(float64) float64{
		"sin": math.Sin, "cos": math.Cos, "tan": math.Tan,
		"sinh": math.Sinh, "cosh": math.Cosh, "tanh": math.Tanh,
	}
	for name, fn := range trig {
		ident(rt, func(c *interp.Call) (string, error) {
			x := interp.Num(c.Arg(0))
			if strings.EqualFold(c.Prop, "deg") {
				x *= math.Pi / 180
			}
			return num(fn(x))
		}, name)
	}
	inverse := map[string]func(float64) float64{"asin": math.Asin, "acos": math.Acos, "atan": math.Atan}
	for name, fn := range inverse {
		ident(rt, func(c *interp.Call) (string, error) { return num(degrees(c, fn(interp.Num(c.Arg(0))))) }, name)
	}
	ident(rt, func(c *interp.Call) (string, error) {
		return num(degrees(c, math.Atan2(interp.Num(c.Arg(0)), interp.Num(c.Arg(1)))))
	}, "atan2")
	ident(rt, func(c *interp.Call) (string, error) {
		return num(math.Hypot(interp.Num(c.Arg(0)), interp.Num(c.Arg(1))))
	}, "hypot")
	ident(rt, func(*interp.Call) (string, error) { return "3.14159265358979323846", nil }, "pi")
	ident(rt, identRound, "round")
	ident(rt, identIsNum, "isnum")
	ident(rt, identBase, "base")
	ident(rt, identBytes, "bytes")
	ident(rt, identMinMax, "min", "max")
	ident(rt, identGcdLcm, "gcd", "lcm")
	ident(rt, identPowmod, "powmod")
	ident(rt, identModinv, "modinv")
	ident(rt, identIntersect, "intersect")
	ident(rt, identLongip, "longip")
	ident(rt, identBitwise, "and", "or", "xor")
	ident(rt, func(c *interp.Call) (string, error) { return u32(^toU32(c.Arg(0))), nil }, "not")
	ident(rt, identBit, "biton", "bitoff", "isbit")
}

func degrees(c *interp.Call, rad float64) float64 {
	if strings.EqualFold(c.Prop, "deg") {
		return rad * 180 / math.Pi
	}
	return rad
}

func identRound(c *interp.Call) (string, error) {
	x := interp.Num(c.Arg(0))
	d := interp.Int(c.Arg(1))
	scale := math.Pow(10, float64(d))
	return num(math.Round(x*scale) / scale)
}

// identIsNum implements $isnum(N,sd,I,J).
func identIsNum(c *interp.Call) (string, error) {
	s := c.Arg(0)
	opts := c.Arg(1)
	if s == "" {
		return interp.Bool(false), nil
	}
	body := s
	if body[0] == '+' || body[0] == '-' {
		if opts != "" && !strings.Contains(opts, "s") {
			return interp.Bool(false), nil
		}
		body = body[1:]
	}
	if strings.Contains(body, ".") && opts != "" && !strings.Contains(opts, "d") {
		return interp.Bool(false), nil
	}
	x, ok := interp.ParseNumber(s)
	if !ok {
		return interp.Bool(false), nil
	}
	if len(c.Args) > 2 && c.Arg(2) != "" && x < interp.Num(c.Arg(2)) {
		return interp.Bool(false), nil
	}
	if len(c.Args) > 3 && c.Arg(3) != "" && x > interp.Num(c.Arg(3)) {
		return interp.Bool(false), nil
	}
	return interp.Bool(true), nil
}

const digits36 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// identBase implements $base(N,inbase,outbase,zeropad,precision), including
// fractional parts.
func identBase(c *interp.Call) (string, error) {
	in, out := interp.Int(c.Arg(1)), interp.Int(c.Arg(2))
	if in < 2 || in > 36 || out < 2 || out > 36 {
		return "", c.InvalidParams()
	}
	s := strings.ToUpper(strings.TrimSpace(c.Arg(0)))
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	ip, fp, _ := strings.Cut(s, ".")

	whole := new(big.Int)
	for _, ch := range ip {
		d := strings.IndexRune(digits36, ch)
		if d < 0 || d >= in {
			break
		}
		whole.Mul(whole, big.NewInt(int64(in)))
		whole.Add(whole, big.NewInt(int64(d)))
	}
	frac, scale := 0.0, 1.0/float64(in)
	for _, ch := range fp {
		d := strings.IndexRune(digits36, ch)
		if d < 0 || d >= in {
			break
		}
		frac += float64(d) * scale
		scale /= float64(in)
	}

	res := strings.ToUpper(whole.Text(out))
	if pad := interp.Int(c.Arg(3)); len(res) < pad {
		res = strings.Repeat("0", pad-len(res)) + res
	}
	if frac > 0 {
		prec := 6
		if len(c.Args) > 4 {
			prec = interp.Int(c.Arg(4))
		}
		var b strings.Builder
		for range prec {
			frac *= float64(out)
			d := int(frac)
			b.WriteByte(digits36[d])
			frac -= float64(d)
			if frac <= 1e-12 {
				break
			}
		}
		if f := strings.TrimRight(b.String(), "0"); f != "" {
			res += "." + f
		}
	}
	if neg && res != "0" {
		res = "-" + res
	}
	return res, nil
}

// identBytes implements $bytes(N,bkmgt3d).suf.
func identBytes(c *interp.Call) (string, error) {
	n := interp.Num(c.Arg(0))
	opts := strings.ToLower(c.Arg(1))
	units := "bkmgt"
	suffixes := []string{"B", "KB", "MB", "GB", "TB"}
	unit := -1
	for i := range units {
		if strings.IndexByte(opts, units[i]) >= 0 {
			unit = i
		}
	}
	if strings.Contains(opts, "3") || unit < 0 && opts != "" {
		unit = 0
		for unit < 4 && math.Abs(n) >= 1000 {
			n /= 1024
			unit++
		}
		s := strconv.FormatFloat(n, 'f', max(0, 2-int(math.Log10(math.Max(1, math.Abs(n))))), 64)
		if unit == 0 {
			s = strconv.FormatFloat(n, 'f', 0, 64)
		}
		return withSuffix(c, s, suffixes[unit]), nil
	}
	if unit < 0 {
		return withSuffix(c, commas(strconv.FormatFloat(math.Trunc(n), 'f', 0, 64)), "B"), nil
	}
	n /= math.Pow(1024, float64(unit))
	dec := 0
	if strings.Contains(opts, "d") {
		dec = 2
	}
	s := strconv.FormatFloat(n, 'f', dec, 64)
	ip, fp, hasFrac := strings.Cut(s, ".")
	s = commas(ip)
	if hasFrac {
		if fp = strings.TrimRight(fp, "0"); fp != "" {
			s += "." + fp
		}
	}
	return withSuffix(c, s, suffixes[unit]), nil
}

func withSuffix(c *interp.Call, s, suffix string) string {
	if strings.EqualFold(c.Prop, "suf") {
		return s + suffix
	}
	return s
}

// commas inserts thousands separators into an integer string.
func commas(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// identMinMax implements $min/$max with the alpha, alphacs, alnum, alnumcs
// and nick comparison properties.
func identMinMax(c *interp.Call) (string, error) {
	vals := c.Args
	if len(vals) == 1 {
		vals = strings.Fields(vals[0])
	}
	if len(vals) == 0 {
		return "", nil
	}
	prop := strings.ToLower(c.Prop)
	less := func(a, b string) bool { return interp.Num(a) < interp.Num(b) }
	switch prop {
	case "alpha", "nick":
		less = func(a, b string) bool { return strings.ToLower(a) < strings.ToLower(b) }
	case "alphacs":
		less = func(a, b string) bool { return a < b }
	case "alnum":
		less = func(a, b string) bool { return naturalLess(strings.ToLower(a), strings.ToLower(b)) }
	case "alnumcs":
		less = naturalLess
	}
	sorted := append([]string(nil), vals...)
	sort.SliceStable(sorted, func(i, j int) bool { return less(sorted[i], sorted[j]) })
	if c.Name == "max" {
		return sorted[len(sorted)-1], nil
	}
	return sorted[0], nil
}

// naturalLess compares strings treating runs of digits as numbers.
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		if isDigit(a[0]) && isDigit(b[0]) {
			i, j := 0, 0
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			for j < len(b) && isDigit(b[j]) {
				j++
			}
			na, _ := strconv.ParseFloat(a[:i], 64)
			nb, _ := strconv.ParseFloat(b[:j], 64)
			if na != nb {
				return na < nb
			}
			a, b = a[i:], b[j:]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func identGcdLcm(c *interp.Call) (string, error) {
	acc := new(big.Int)
	for i, a := range c.Args {
		n, ok := new(big.Int).SetString(strings.TrimSpace(a), 10)
		if !ok {
			n = big.NewInt(int64(interp.Num(a)))
		}
		n.Abs(n)
		switch {
		case i == 0:
			acc.Set(n)
		case c.Name == "gcd":
			acc.GCD(nil, nil, acc, n)
		default:
			if acc.Sign() == 0 || n.Sign() == 0 {
				acc.SetInt64(0)
				continue
			}
			g := new(big.Int).GCD(nil, nil, acc, n)
			acc.Mul(acc, n).Div(acc, g)
		}
	}
	return acc.String(), nil
}

func bigArg(c *interp.Call, i int) *big.Int {
	n, ok := new(big.Int).SetString(strings.TrimSpace(c.Arg(i)), 10)
	if !ok {
		return big.NewInt(int64(interp.Num(c.Arg(i))))
	}
	return n
}

func identPowmod(c *interp.Call) (string, error) {
	m := bigArg(c, 2)
	if m.Sign() == 0 {
		return "0", nil
	}
	return new(big.Int).Exp(bigArg(c, 0), bigArg(c, 1), m).String(), nil
}

func identModinv(c *interp.Call) (string, error) {
	r := new(big.Int).ModInverse(bigArg(c, 0), bigArg(c, 1))
	if r == nil {
		return "0", nil
	}
	return r.String(), nil
}

// identIntersect returns "x y" where two lines (or rays) intersect, or $null.
func identIntersect(c *interp.Call) (string, error) {
	var p [8]float64
	for i := range p {
		p[i] = interp.Num(c.Arg(i))
	}
	method := strings.ToLower(c.Arg(8))
	d := (p[0]-p[2])*(p[5]-p[7]) - (p[1]-p[3])*(p[4]-p[6])
	if d == 0 {
		return "", nil
	}
	t := ((p[0]-p[4])*(p[5]-p[7]) - (p[1]-p[5])*(p[4]-p[6])) / d
	u := -((p[0]-p[2])*(p[1]-p[5]) - (p[1]-p[3])*(p[0]-p[4])) / d
	inLine := func(v float64) bool { return v >= 0 && v <= 1 }
	inRay := func(v float64) bool { return v >= 0 }
	okT, okU := inLine, inLine
	switch method {
	case "lr":
		okU = inRay
	case "rl":
		okT = inRay
	case "rr":
		okT, okU = inRay, inRay
	}
	if !okT(t) || !okU(u) {
		return "", nil
	}
	x := p[0] + t*(p[2]-p[0])
	y := p[1] + t*(p[3]-p[1])
	return interp.FormatNumber(x) + " " + interp.FormatNumber(y), nil
}

func identLongip(c *interp.Call) (string, error) {
	s := strings.TrimSpace(c.Arg(0))
	if strings.Contains(s, ".") {
		a, err := netip.ParseAddr(s)
		if err != nil || !a.Is4() {
			return "", nil
		}
		b := a.As4()
		return strconv.FormatUint(uint64(b[0])<<24|uint64(b[1])<<16|uint64(b[2])<<8|uint64(b[3]), 10), nil
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return "", nil
	}
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}).String(), nil
}

func toU32(s string) uint32 {
	x := interp.Num(s)
	if x < 0 {
		return uint32(int64(x))
	}
	return uint32(uint64(x))
}

func u32(v uint32) string { return strconv.FormatUint(uint64(v), 10) }

func identBitwise(c *interp.Call) (string, error) {
	a, b := toU32(c.Arg(0)), toU32(c.Arg(1))
	switch c.Name {
	case "and":
		return u32(a & b), nil
	case "or":
		return u32(a | b), nil
	}
	return u32(a ^ b), nil
}

// identBit implements $biton, $bitoff and $isbit, where bit 1 is the lowest.
func identBit(c *interp.Call) (string, error) {
	a := toU32(c.Arg(0))
	n := interp.Int(c.Arg(1))
	if n < 1 || n > 32 {
		return "", c.InvalidParams()
	}
	mask := uint32(1) << (n - 1)
	switch c.Name {
	case "biton":
		return u32(a | mask), nil
	case "bitoff":
		return u32(a &^ mask), nil
	}
	if a&mask != 0 {
		return "1", nil
	}
	return "0", nil
}
