package control

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

// Version is the mIRC version the runtime implements ($version).
const Version = "7.85"

// settings are option values scripts can read and change.
type settings struct {
	fullName, email string
	doNotDisturb    bool
	colors          [100]int // RGB palette
	colorNames      map[string]int
}

// defaultPalette is mIRC's default colour palette for indexes 0-15.
var defaultPalette = []int{
	0xFFFFFF, 0x000000, 0x7F0000, 0x009300, 0x0000FF, 0x00007F, 0x9C009C, 0x007FFC,
	0x00FFFF, 0x00FC00, 0x939300, 0xFFFF00, 0xFC0000, 0xFF00FF, 0x7F7F7F, 0xD2D2D2,
}

// defaultColorNames maps mIRC's colour option names to their default indexes.
var defaultColorNames = map[string]int{
	"background": 0, "action text": 6, "ctcp text": 4, "highlight text": 4,
	"info text": 2, "info2 text": 12, "invite text": 3, "join text": 3,
	"kick text": 3, "mode text": 3, "nick text": 3, "normal text": 1,
	"notice text": 5, "notify text": 2, "other text": 6, "own text": 1,
	"part text": 3, "quit text": 2, "topic text": 3, "wallops text": 5,
	"whois text": 1, "editbox": 0, "editbox text": 1, "listbox": 0,
	"listbox text": 1, "gray text": 14, "title text": 1, "inactive": 14,
}

func defaultSettings() settings {
	s := settings{colorNames: map[string]int{}}
	for i, rgb := range defaultPalette {
		s.colors[i] = toBGR(rgb)
	}
	for k, v := range defaultColorNames {
		s.colorNames[k] = v
	}
	return s
}

// toBGR converts 0xRRGGBB to mIRC's $rgb value order (blue in the high byte).
func toBGR(rgb int) int {
	r, g, b := rgb>>16&0xff, rgb>>8&0xff, rgb&0xff
	return b<<16 | g<<8 | r
}

// Starting marks on START processing for $starting.
func (c *Control) Starting(on bool) { c.starting = on }

func (c *Control) installSystem() {
	rt := c.rt
	str := func(name, v string) { rt.RegisterIdent(name, func(*interp.Call) (string, error) { return v, nil }) }
	str("version", Version)
	str("sslversion", "Go crypto/tls (TLS 1.3)")
	str("ssldll", "")
	str("ssllibdll", "")
	str("maxlenl", "8192")
	str("maxlenm", "4096")
	str("maxlens", "2048")
	rt.RegisterIdent("cmdline", func(*interp.Call) (string, error) { return strings.Join(os.Args, " "), nil })
	rt.RegisterIdent("starting", func(*interp.Call) (string, error) { return boolDigit(c.starting), nil })
	rt.RegisterIdent("exiting", func(*interp.Call) (string, error) { return strconv.Itoa(c.exiting), nil })
	rt.RegisterIdent("sslready", func(*interp.Call) (string, error) { return interp.Bool(true), nil })
	rt.RegisterIdent("remote", func(*interp.Call) (string, error) {
		n := 0
		for bit, kind := range map[int]string{1: "ctcp", 2: "on", 4: "raw"} {
			if rt.EventsEnabled(kind) {
				n |= bit
			}
		}
		return strconv.Itoa(n), nil
	})
	rt.RegisterIdent("fullname", func(*interp.Call) (string, error) { return c.settings.fullName, nil })
	rt.RegisterIdent("emailaddr", func(*interp.Call) (string, error) { return c.settings.email, nil })
	rt.RegisterCommand("fullname", interp.Command{Fn: func(cc *interp.CmdCall) error { c.settings.fullName = cc.Args; return nil }})
	rt.RegisterCommand("emailaddr", interp.Command{Fn: func(cc *interp.CmdCall) error { c.settings.email = cc.Args; return nil }})
	rt.RegisterIdent("donotdisturb", func(*interp.Call) (string, error) { return interp.Bool(c.settings.doNotDisturb), nil })
	rt.RegisterCommand("donotdisturb", interp.Command{Fn: func(cc *interp.CmdCall) error {
		c.settings.doNotDisturb = !strings.EqualFold(strings.TrimSpace(cc.Args), "off")
		return nil
	}})
	rt.RegisterIdent("darkmode", func(*interp.Call) (string, error) { return interp.Bool(false), nil })
	rt.RegisterIdent("lock", func(*interp.Call) (string, error) { return interp.Bool(false), nil })
	rt.RegisterIdent("locked", func(*interp.Call) (string, error) { return interp.Bool(false), nil })
	rt.RegisterIdent("codepage", identCodepage)
	rt.RegisterIdent("color", c.identColor)
	rt.RegisterCommand("color", interp.Command{Fn: c.cmdColor})
	rt.RegisterIdent("cb", identClipboard)
	rt.RegisterCommand("clipboard", interp.Command{Fn: cmdClipboard})
	rt.RegisterIdent("ansi2mirc", func(cl *interp.Call) (string, error) { return ANSIToMIRC(cl.Arg(0)), nil })
	rt.RegisterCommand("exit", interp.Command{Fn: func(cc *interp.CmdCall) error {
		sw, _ := cc.Switches()
		c.exiting = 1
		if strings.Contains(sw, "r") {
			c.exiting = 2
		}
		if rt.Exit != nil {
			rt.Exit(c.exiting == 2)
		}
		return nil
	}})
	rt.RegisterCommand("resetidle", interp.Command{Fn: func(*interp.CmdCall) error { return nil }})
	if c.irc != nil {
		rt.RegisterIdent("sslcertvalid", func(cl *interp.Call) (string, error) {
			conn := c.irc.FrameConn(cl.F)
			return interp.Bool(conn.Spec.TLS && conn.Status != 0), nil
		})
	}
}

func boolDigit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func identCodepage(cl *interp.Call) (string, error) {
	pages := []string{"UTF-8"}
	if !cl.Paren {
		return pages[0], nil
	}
	n := interp.Int(cl.Arg(0))
	if n == 0 {
		return strconv.Itoa(len(pages)), nil
	}
	if n == 1 {
		switch strings.ToLower(cl.Prop) {
		case "id":
			return "65001", nil
		case "desc":
			return "Unicode (UTF-8)", nil
		}
		return pages[0], nil
	}
	return "", nil
}

// identColor implements $color(name|N).dd.
func (c *Control) identColor(cl *interp.Call) (string, error) {
	arg := strings.ToLower(cl.Arg(0))
	if n, ok := interp.ParseNumber(arg); ok {
		i := int(n)
		if i < 0 || i >= len(c.settings.colors) {
			return "", nil
		}
		return strconv.Itoa(c.settings.colors[i]), nil
	}
	idx := -1
	if v, ok := c.settings.colorNames[arg]; ok {
		idx = v
	} else {
		best := ""
		for k, v := range c.settings.colorNames {
			if strings.HasPrefix(k, arg) && (best == "" || k < best) {
				best, idx = k, v
			}
		}
	}
	if idx < 0 {
		return "", nil
	}
	if strings.EqualFold(cl.Prop, "dd") {
		return pad2(idx), nil
	}
	return strconv.Itoa(idx), nil
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// cmdColor implements /color [-lsr] <name|N> <index|rgb>.
func (c *Control) cmdColor(cc *interp.CmdCall) error {
	sw, rest := cc.Switches()
	if strings.Contains(sw, "r") && strings.TrimSpace(rest) == "" {
		c.settings = defaultSettings()
		return nil
	}
	f := strings.Fields(rest)
	if len(f) < 2 {
		return cc.InvalidParams()
	}
	value := interp.Int(f[len(f)-1])
	name := strings.ToLower(strings.Join(f[:len(f)-1], " "))
	if n, ok := interp.ParseNumber(name); ok && strings.Contains(sw, "l") {
		if i := int(n); i >= 0 && i < len(c.settings.colors) {
			c.settings.colors[i] = value
		}
		return nil
	}
	c.settings.colorNames[name] = value
	return nil
}

// clipboard commands per platform; reading and writing use external tools so
// the runtime stays free of cgo.
func clipboardRead() string {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("powershell", "-NoProfile", "-Command", "Get-Clipboard -Raw")
	case "darwin":
		cmd = exec.Command("pbpaste")
	default:
		cmd = exec.Command("sh", "-c", "wl-paste -n 2>/dev/null || xclip -selection clipboard -o 2>/dev/null || xsel -b -o")
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\r\n")
}

func clipboardWrite(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("powershell", "-NoProfile", "-Command", "$input | Set-Clipboard")
	case "darwin":
		cmd = exec.Command("pbcopy")
	default:
		cmd = exec.Command("sh", "-c", "wl-copy 2>/dev/null || xclip -selection clipboard 2>/dev/null || xsel -b -i")
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// identClipboard implements $cb and $cb(N,[u],[%var|&binvar]).len.
func identClipboard(cl *interp.Call) (string, error) {
	text := clipboardRead()
	if !cl.Paren {
		return strings.ReplaceAll(text, "\r\n", " "), nil
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if text == "" {
		lines = nil
	}
	n := interp.Int(cl.Arg(0))
	var v string
	switch {
	case n == 0:
		if strings.EqualFold(cl.Prop, "len") {
			return strconv.Itoa(len(text)), nil
		}
		return strconv.Itoa(len(lines)), nil
	case n == -1:
		v = text
	case n >= 1 && n <= len(lines):
		v = lines[n-1]
	}
	if strings.EqualFold(cl.Prop, "len") {
		return strconv.Itoa(len(v)), nil
	}
	if target := cl.Arg(len(cl.Args) - 1); len(cl.Args) > 1 && strings.HasPrefix(target, "%") {
		cl.F.SetVar(target, v, false)
		return strconv.Itoa(len(v)), nil
	}
	return v, nil
}

// cmdClipboard implements /clipboard [-an] <text>.
func cmdClipboard(cc *interp.CmdCall) error {
	sw, text := cc.Switches()
	if strings.Contains(sw, "a") {
		text = clipboardRead() + text
	}
	if strings.Contains(sw, "n") {
		text += "\r\n"
	}
	if err := clipboardWrite(text); err != nil {
		return cc.Errorf("unable to set clipboard")
	}
	return nil
}

// ansiColors maps ANSI colour numbers (0-7) to mIRC colour indexes.
var ansiColors = [8]int{1, 4, 3, 7, 2, 6, 10, 0}
var ansiBright = [8]int{14, 4, 9, 8, 12, 13, 11, 0}

// ANSIToMIRC converts ANSI SGR escape sequences to mIRC control codes.
func ANSIToMIRC(s string) string {
	var b strings.Builder
	fg, bg := -1, -1
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b || i+1 >= len(s) || s[i+1] != '[' {
			b.WriteByte(s[i])
			continue
		}
		end := strings.IndexByte(s[i:], 'm')
		if end < 0 {
			b.WriteByte(s[i])
			continue
		}
		params := strings.Split(s[i+2:i+end], ";")
		i += end
		colour := false
		for _, p := range params {
			n, _ := strconv.Atoi(p)
			switch {
			case n == 0:
				b.WriteByte(0x0f)
				fg, bg = -1, -1
			case n == 1:
				b.WriteByte(0x02)
			case n == 3:
				b.WriteByte(0x1d)
			case n == 4:
				b.WriteByte(0x1f)
			case n == 7:
				b.WriteByte(0x16)
			case n >= 30 && n <= 37:
				fg, colour = ansiColors[n-30], true
			case n >= 90 && n <= 97:
				fg, colour = ansiBright[n-90], true
			case n >= 40 && n <= 47:
				bg, colour = ansiColors[n-40], true
			}
		}
		if colour && fg >= 0 {
			b.WriteByte(0x03)
			b.WriteString(pad2(fg))
			if bg >= 0 {
				b.WriteString("," + pad2(bg))
			}
		}
	}
	return b.String()
}
