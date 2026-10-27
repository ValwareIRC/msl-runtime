package builtins

import (
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

func installOutput(rt *interp.Runtime) {
	rt.RegisterCommand("echo", interp.Command{Fn: cmdEcho})
}

// ActiveWindow is the window name used for echoes to the active window.
const ActiveWindow = "Active"

// cmdEcho implements /echo [color] [-cdeghiNtsaqlbfnmr] [color name] [window] <text>.
func cmdEcho(c *interp.CmdCall) error {
	text := c.Args
	sw := ""
	// A leading colour number may come before or after the switches.
	if word, rest, _ := strings.Cut(strings.TrimLeft(text, " "), " "); isColor(word) && rest != "" {
		text = rest
	}
	sw, text = interp.SplitSwitches(text)
	if strings.Contains(sw, "q") && !c.F.Show {
		return nil
	}
	if strings.Contains(sw, "c") {
		// -c takes a colour name such as "info" before the window and text.
		_, text, _ = strings.Cut(text, " ")
	}
	if word, rest, _ := strings.Cut(strings.TrimLeft(text, " "), " "); isColor(word) && rest != "" {
		text = rest
	}

	window := ActiveWindow
	switch {
	case strings.ContainsAny(sw, "sd"):
		window = interp.StatusWindow
	case strings.Contains(sw, "a"):
	default:
		if word, rest, _ := strings.Cut(strings.TrimLeft(text, " "), " "); isWindowName(word) {
			window, text = word, rest
		}
	}
	if text == "" {
		return c.Errorf("insufficient parameters")
	}
	c.F.RT.Out.Echo(window, text)
	return nil
}

func isColor(w string) bool {
	if w == "" || len(w) > 2 {
		return false
	}
	for i := 0; i < len(w); i++ {
		if w[i] < '0' || w[i] > '9' {
			return false
		}
	}
	return true
}

func isWindowName(w string) bool {
	return len(w) > 1 && strings.IndexByte("#@=&!+", w[0]) >= 0
}
