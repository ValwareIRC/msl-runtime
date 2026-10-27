package conformance

import (
	"fmt"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// Capture is an interp.Output that records echoed text and errors in order.
type Capture struct {
	Lines []string
}

// Echo records the text of an echo, ignoring the window.
func (c *Capture) Echo(_, text string) { c.Lines = append(c.Lines, text) }

// Error records a script error line.
func (c *Capture) Error(text string) { c.Lines = append(c.Lines, text) }

// Setup prepares a runtime for a case; the caller installs builtins.
type Setup func(rt *interp.Runtime)

// Run executes a case and returns what it printed.
func Run(c Case, setup Setup) ([]string, error) {
	out := &Capture{}
	rt := interp.New(out)
	setup(rt)
	for name, src := range c.Files {
		if !strings.HasSuffix(name, ".mrc") {
			continue
		}
		if _, errs := rt.LoadSource(name, src, script.Remote); len(errs) > 0 {
			return nil, fmt.Errorf("%s: %v", name, errs)
		}
	}
	if !rt.HasAlias(nil, "test") {
		return nil, fmt.Errorf("%s: no test alias", c.Name)
	}
	_ = rt.CallAlias("test", nil)
	return out.Lines, nil
}

// Diff describes how got differs from want, or returns "" when they match.
func Diff(want, got []string) string {
	var b strings.Builder
	for i := 0; i < max(len(want), len(got)); i++ {
		w, g := "<none>", "<none>"
		if i < len(want) {
			w = want[i]
		}
		if i < len(got) {
			g = got[i]
		}
		if w != g {
			fmt.Fprintf(&b, "  line %d: want %q\n          got  %q\n", i+1, w, g)
		}
	}
	return b.String()
}
