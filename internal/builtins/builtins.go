// Package builtins provides mIRC's built-in commands and identifiers that
// need nothing beyond the interpreter: output, text, numbers, tokens and time.
// Feature packages (irc, sockets, storage, ...) register their own.
package builtins

import (
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

// Install registers every built-in in this package.
func Install(rt *interp.Runtime) {
	installOutput(rt)
	installText(rt)
	installNumbers(rt)
	installTokens(rt)
	installTime(rt)
}

// ident is shorthand for registering several names to one function.
func ident(rt *interp.Runtime, fn interp.Ident, names ...string) {
	for _, n := range names {
		rt.RegisterIdent(n, fn)
	}
}

// cs reports whether an identifier name is the case-sensitive variant.
func cs(name string) bool { return strings.HasSuffix(name, "cs") }

// num formats a number result.
func num(x float64) (string, error) { return interp.FormatNumber(x), nil }
