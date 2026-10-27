package interp

import (
	"math"
	"strconv"
	"strings"
)

// Decimals is how many decimal places mIRC keeps in calculations (7.47+).
const Decimals = 6

// ParseNumber parses an mIRC number: optional sign, digits and an optional
// decimal part. Exponents, hex and surrounding text are not numbers.
func ParseNumber(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	i := 0
	if s[0] == '-' || s[0] == '+' {
		i++
	}
	digits, dot := 0, false
	for ; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '.' && !dot:
			dot = true
		default:
			return 0, false
		}
	}
	if digits == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimPrefix(s, "+"), 64)
	return v, err == nil
}

// Num parses the leading number of s the way mIRC's numeric identifiers do:
// leading spaces are skipped and anything after the number is ignored;
// text without a number is zero.
func Num(s string) float64 {
	s = strings.TrimLeft(s, " ")
	end := 0
	if end < len(s) && (s[end] == '-' || s[end] == '+') {
		end++
	}
	dot := false
	for end < len(s) {
		c := s[end]
		if c >= '0' && c <= '9' {
			end++
			continue
		}
		if c == '.' && !dot {
			dot = true
			end++
			continue
		}
		break
	}
	v, err := strconv.ParseFloat(strings.TrimRight(strings.TrimPrefix(s[:end], "+"), "."), 64)
	if err != nil {
		return 0
	}
	return v
}

// Int is Num truncated to an integer.
func Int(s string) int { return int(Num(s)) }

func isWhole(x float64) bool { return x == math.Trunc(x) }

// FormatNumber renders a number as mIRC does: integers without a decimal
// point, other values rounded to six places with trailing zeros removed.
func FormatNumber(x float64) string {
	switch {
	case math.IsNaN(x), math.IsInf(x, 0):
		return "0"
	case isWhole(x) && math.Abs(x) < 1e21:
		if x == 0 {
			return "0"
		}
		return strconv.FormatFloat(x, 'f', 0, 64)
	}
	s := strconv.FormatFloat(x, 'f', Decimals, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" {
		return "0"
	}
	return s
}

// SimpleMath applies the single operation of "%x = N op N" assignments.
// Anything that is not exactly two numbers around an operator is returned
// unchanged.
func SimpleMath(v string) string {
	parts := strings.Fields(v)
	if len(parts) != 3 {
		return v
	}
	a, ok1 := ParseNumber(parts[0])
	b, ok2 := ParseNumber(parts[2])
	if !ok1 || !ok2 {
		return v
	}
	r, ok := applyOp(parts[1], a, b)
	if !ok {
		return v
	}
	return FormatNumber(r)
}

func applyOp(op string, a, b float64) (float64, bool) {
	switch op {
	case "+":
		return a + b, true
	case "-":
		return a - b, true
	case "*":
		return a * b, true
	case "/":
		if b == 0 {
			return 0, true
		}
		return a / b, true
	case "//":
		if b == 0 {
			return 0, true
		}
		return math.Floor(a / b), true
	case "%":
		if b == 0 {
			return 0, true
		}
		return math.Mod(a, b), true
	case "^":
		return math.Pow(a, b), true
	}
	return 0, false
}
