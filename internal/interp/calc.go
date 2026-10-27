package interp

import (
	"strconv"
	"strings"
)

// Calc evaluates a $calc expression: numbers, parentheses, unary signs and
// the operators ^, * / // %, + -, from highest to lowest precedence, each
// left-associative. Text that is not a number counts as zero, as do empty
// operands.
func Calc(expr string) float64 {
	p := &calcParser{s: expr}
	v := p.sum()
	return v
}

type calcParser struct {
	s string
	i int
}

func (p *calcParser) skip() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *calcParser) peekOp() string {
	p.skip()
	if p.i >= len(p.s) {
		return ""
	}
	if strings.HasPrefix(p.s[p.i:], "//") {
		return "//"
	}
	return p.s[p.i : p.i+1]
}

func (p *calcParser) sum() float64 {
	v := p.product()
	for {
		op := p.peekOp()
		if op != "+" && op != "-" {
			return v
		}
		p.i += len(op)
		r, _ := applyOp(op, v, p.product())
		v = r
	}
}

func (p *calcParser) product() float64 {
	v := p.power()
	for {
		op := p.peekOp()
		if op != "*" && op != "/" && op != "//" && op != "%" {
			return v
		}
		p.i += len(op)
		r, _ := applyOp(op, v, p.power())
		v = r
	}
}

func (p *calcParser) power() float64 {
	v := p.unary()
	for p.peekOp() == "^" {
		p.i++
		r, _ := applyOp("^", v, p.unary())
		v = r
	}
	return v
}

func (p *calcParser) unary() float64 {
	switch p.peekOp() {
	case "-":
		p.i++
		return -p.unary()
	case "+":
		p.i++
		return p.unary()
	}
	return p.primary()
}

func (p *calcParser) primary() float64 {
	p.skip()
	if p.i >= len(p.s) {
		return 0
	}
	if p.s[p.i] == '(' {
		p.i++
		v := p.sum()
		if p.peekOp() == ")" {
			p.i++
		}
		return v
	}
	start := p.i
	for p.i < len(p.s) && (p.s[p.i] >= '0' && p.s[p.i] <= '9' || p.s[p.i] == '.') {
		p.i++
	}
	if p.i == start {
		// Skip a stray token so the parse always advances.
		for p.i < len(p.s) && strings.IndexByte(" \t()+-*/%^", p.s[p.i]) < 0 {
			p.i++
		}
		if p.i == start {
			p.i++
		}
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimRight(p.s[start:p.i], "."), 64)
	if err != nil {
		return 0
	}
	return v
}
