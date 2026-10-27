package interp

import (
	"strings"
	"unicode/utf8"
)

// WildMatch matches text against an mIRC wildcard pattern, where * matches
// any run of characters and ? matches one character. Matching covers the
// whole text and is case-insensitive unless cs is set.
func WildMatch(pattern, text string, cs bool) bool {
	if !cs {
		pattern, text = strings.ToLower(pattern), strings.ToLower(text)
	}
	return wild(pattern, text, false)
}

// WildMatchWords is WildMatch with mIRC's event matchtext extension: & matches
// exactly one word.
func WildMatchWords(pattern, text string, cs bool) bool {
	if !cs {
		pattern, text = strings.ToLower(pattern), strings.ToLower(text)
	}
	return wild(pattern, text, true)
}

func wild(p, t string, words bool) bool {
	// Backtracking over the last star keeps this linear for typical patterns.
	var starP, starT = -1, 0
	pi, ti := 0, 0
	for ti < len(t) {
		if pi < len(p) {
			switch c := p[pi]; {
			case c == '*':
				starP, starT = pi, ti
				pi++
				continue
			case words && c == '&' && atWordStart(t, ti) && (pi+1 == len(p) || p[pi+1] == ' '):
				end := ti
				for end < len(t) && t[end] != ' ' {
					end++
				}
				if end > ti && wild(p[pi+1:], t[end:], words) {
					return true
				}
			case c == '?':
				_, n := utf8.DecodeRuneInString(t[ti:])
				pi++
				ti += n
				continue
			default:
				pr, pn := utf8.DecodeRuneInString(p[pi:])
				tr, tn := utf8.DecodeRuneInString(t[ti:])
				if pr == tr {
					pi += pn
					ti += tn
					continue
				}
			}
		}
		if starP < 0 {
			return false
		}
		_, n := utf8.DecodeRuneInString(t[starT:])
		starT += n
		pi, ti = starP+1, starT
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

func atWordStart(t string, i int) bool { return i == 0 || t[i-1] == ' ' }
