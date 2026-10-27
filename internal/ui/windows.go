package ui

import (
	"image"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/regex"
	"github.com/ValwareIRC/msl-runtime/internal/script"
	"github.com/ValwareIRC/msl-runtime/internal/storage"
)

// Line is one line of a custom window.
type Line struct {
	Text     string
	Color    int // -1 for the default colour
	Selected bool
}

// Window is a custom @window.
type Window struct {
	Name     string
	Wid      int
	Cid      int
	Title    string
	X, Y     int
	W, H     int
	Lines    []Line
	Side     []Line // side listbox
	Listbox  bool
	SideW    int
	Editbox  bool
	Picture  bool
	Pic      *image.RGBA
	Sorted   bool
	SideSort bool
	Hidden   bool
	State    string // normal, minimized, maximized, hidden
	OnTop    bool
	Desktop  bool
	Font     string
	FontSize int
	Bold     bool
	Command  string // default command for editbox input
	EditText string // current editbox contents
	// EditSeq counts script changes to EditText (/editbox), so a display
	// knows when to replace what the user typed.
	EditSeq int
	// HidePrefix hides the @ in the title (/window -k0).
	HidePrefix bool
	Popup      string // menu name or popup file
	Buffer     int
	Tabs       []int
	AnyConn    bool
	Progress   [2]int
}

// Mouse is the pointer state reported for picture windows and dialogs ($mouse).
type Mouse struct {
	Window string
	X, Y   int
	Key    int // bit 1 left button, 2 ctrl, 4 shift, 8 alt
	Lb     int
	DX, DY int
}

func (u *UI) window(name string) *Window {
	for _, w := range u.windows {
		if strings.EqualFold(w.Name, name) {
			return w
		}
	}
	return nil
}

// Windows lists custom windows.
func (u *UI) Windows() []*Window { return u.windows }

// Window returns a custom window by name.
func (u *UI) Window(name string) *Window { return u.window(name) }

func (u *UI) setActive(name string) {
	if strings.EqualFold(u.active, name) {
		return
	}
	u.lactive, u.active = u.active, name
}

// output wraps the runtime output so echoes to @windows land in their buffers.
type output struct {
	next interp.Output
	u    *UI
}

func (o *output) Echo(window, text string) {
	if w := o.u.window(window); w != nil {
		o.u.addLine(w, Line{Text: text, Color: -1}, false)
		return
	}
	o.next.Echo(window, text)
}

func (o *output) Error(text string) { o.next.Error(text) }

func (u *UI) addLine(w *Window, l Line, side bool) {
	if side {
		w.Side = append(w.Side, l)
		if w.SideSort {
			sortLines(w.Side)
		}
	} else {
		w.Lines = append(w.Lines, l)
		if w.Sorted {
			sortLines(w.Lines)
		}
		if w.Buffer > 0 && len(w.Lines) > w.Buffer {
			w.Lines = w.Lines[len(w.Lines)-w.Buffer:]
		}
	}
	u.Front.WindowChanged(w)
}

func sortLines(ls []Line) {
	sort.SliceStable(ls, func(i, j int) bool { return strings.ToLower(ls[i].Text) < strings.ToLower(ls[j].Text) })
}

func (u *UI) installWindows() {
	rt := u.rt
	rt.Out = &output{next: rt.Out, u: u}
	if st := storage.From(rt); st != nil {
		st.Windows = u
	}
	rt.RegisterCommand("window", interp.Command{Fn: u.cmdWindow})
	for _, name := range []string{"aline", "cline", "dline", "iline", "rline", "sline"} {
		name := name
		rt.RegisterCommand(name, interp.Command{Fn: func(c *interp.CmdCall) error { return u.cmdLine(c, name) }})
	}
	rt.RegisterCommand("renwin", interp.Command{Fn: func(c *interp.CmdCall) error {
		f := strings.SplitN(strings.TrimSpace(c.Args), " ", 3)
		if len(f) < 2 {
			return c.InvalidParams()
		}
		w := u.window(f[0])
		if w == nil {
			return c.Errorf("'%s' no such window", f[0])
		}
		w.Name = f[1]
		if len(f) == 3 {
			w.Title = f[2]
		}
		u.Front.WindowChanged(w)
		return nil
	}})
	rt.RegisterCommand("clear", interp.Command{Fn: func(c *interp.CmdCall) error {
		sw, rest := c.Switches()
		if w := u.window(strings.TrimSpace(rest)); w != nil {
			switch {
			case strings.Contains(sw, "c"):
				delete(u.clicks, strings.ToLower(w.Name))
			case strings.Contains(sw, "l"):
				w.Side = nil
			case w.Pic != nil:
				w.Pic = newCanvas(w.Pic.Rect.Dx(), w.Pic.Rect.Dy())
			default:
				w.Lines = nil
			}
			u.Front.WindowChanged(w)
		}
		return nil
	}})
	rt.RegisterCommand("clearall", interp.Command{Fn: func(c *interp.CmdCall) error {
		for _, w := range u.windows {
			w.Lines, w.Side = nil, nil
			u.Front.WindowChanged(w)
		}
		return nil
	}})
	rt.RegisterCommand("linesep", interp.Command{Fn: func(c *interp.CmdCall) error {
		_, rest := c.Switches()
		target := strings.TrimSpace(rest)
		if target == "" {
			target = interp.StatusWindow
		}
		rt.Out.Echo(target, "-")
		return nil
	}})
	rt.RegisterCommand("loadbuf", interp.Command{Fn: u.cmdLoadbuf})
	rt.RegisterCommand("savebuf", interp.Command{Fn: u.cmdSavebuf})
	rt.RegisterCommand("font", interp.Command{Fn: u.cmdFont})
	rt.RegisterCommand("background", interp.Command{Fn: func(*interp.CmdCall) error { return nil }})
	rt.RegisterCommand("flash", interp.Command{Fn: func(*interp.CmdCall) error { return nil }})
	rt.RegisterCommand("log", interp.Command{Fn: func(*interp.CmdCall) error { return nil }})
	rt.RegisterCommand("timestamp", interp.Command{Fn: func(*interp.CmdCall) error { return nil }})
	rt.RegisterCommand("strip", interp.Command{Fn: func(*interp.CmdCall) error { return nil }})
	rt.RegisterCommand("channel", interp.Command{Fn: func(*interp.CmdCall) error { return nil }})
	rt.RegisterCommand("editbox", interp.Command{Fn: u.cmdEditbox})
	rt.RegisterIdent("editbox", u.identEditbox)

	rt.RegisterIdent("window", u.identWindow)
	rt.RegisterIdent("line", u.identLine)
	rt.RegisterIdent("fline", u.identFline)
	rt.RegisterIdent("flinen", func(*interp.Call) (string, error) { return strconv.Itoa(u.flinen), nil })
	rt.RegisterIdent("sline", u.identSline)
	rt.RegisterIdent("active", func(*interp.Call) (string, error) { return u.active, nil })
	rt.RegisterIdent("lactive", func(*interp.Call) (string, error) { return u.lactive, nil })
	rt.RegisterIdent("appactive", func(*interp.Call) (string, error) { return interp.Bool(u.appActive), nil })
	for _, n := range []string{"activecid", "lactivecid"} {
		rt.RegisterIdent(n, func(c *interp.Call) (string, error) { return strconv.Itoa(max(c.F.Cid, 1)), nil })
	}
	widOf := func(name string) string {
		if w := u.window(name); w != nil {
			return strconv.Itoa(w.Wid)
		}
		return "1"
	}
	rt.RegisterIdent("activewid", func(*interp.Call) (string, error) { return widOf(u.active), nil })
	rt.RegisterIdent("lactivewid", func(*interp.Call) (string, error) { return widOf(u.lactive), nil })
	rt.RegisterIdent("wid", func(c *interp.Call) (string, error) {
		if v, ok := c.F.Event.Get("target"); ok {
			return widOf(v), nil
		}
		return widOf(u.active), nil
	})
	rt.RegisterIdent("mouse", u.identMouse)
	for _, n := range []string{"leftwin", "leftwincid", "leftwinwid"} {
		key := n
		rt.RegisterIdent(n, func(c *interp.Call) (string, error) { v, _ := c.F.Event.Get(key); return v, nil })
	}
	for _, n := range []string{"keychar", "keyval", "keyrpt", "menu", "menutype", "menucontext"} {
		key := n
		rt.RegisterIdent(n, func(c *interp.Call) (string, error) { v, _ := c.F.Event.Get(key); return v, nil })
	}
	rt.RegisterIdent("submenu", identSubmenu)
	rt.RegisterIdent("style", identStyle)
}

// cmdWindow implements the parts of /window that apply to custom windows:
// [-switches] [+styles] <@name> [x y [w h]] [/command] [popup] [font [size]].
func (u *UI) cmdWindow(c *interp.CmdCall) error {
	text := strings.TrimSpace(c.Args)
	var sw string
	for strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
		word, rest, _ := strings.Cut(text, " ")
		if word[0] == '-' {
			sw += word[1:]
		}
		text = strings.TrimSpace(rest)
	}
	name, rest, _ := strings.Cut(text, " ")
	if !strings.HasPrefix(name, "@") {
		return c.Errorf("invalid window name '%s'", name)
	}
	w := u.window(name)
	if strings.Contains(sw, "c") {
		if w != nil {
			u.closeWindow(w)
		}
		return nil
	}
	created := w == nil
	if created {
		u.nextWid++
		w = &Window{Name: name, Wid: u.nextWid, Cid: c.F.Cid, X: -1, Y: -1, W: -1, H: -1, State: "normal", Popup: name}
	}
	for i := 0; i < len(sw); i++ {
		ch := sw[i]
		num := ""
		for i+1 < len(sw) && sw[i+1] >= '0' && sw[i+1] <= '9' {
			num += string(sw[i+1])
			i++
		}
		switch ch {
		case 'a', 'v':
			u.setActive(name)
		case 'e':
			w.Editbox = true
		case 'h':
			w.Hidden, w.State = true, "hidden"
		case 'i':
			w.AnyConn = true
		case 'k':
			w.HidePrefix = num == "0"
		case 'j':
			w.Buffer = interp.Int(num)
		case 'l':
			w.Listbox = true
			if num != "" {
				w.SideW = interp.Int(num)
			}
		case 'n':
			w.State = "minimized"
		case 'o':
			w.OnTop = true
		case 'u':
			w.OnTop = false
		case 'p':
			w.Picture = true
		case 'r':
			w.State, w.Hidden = "normal", false
		case 'x':
			w.State = "maximized"
		case 's':
			w.Sorted = true
			sortLines(w.Lines)
		case 'S':
			w.SideSort = true
			sortLines(w.Side)
		case 'd':
			w.Desktop = true
		case 't':
			for _, t := range strings.Split(num+strings.TrimLeft(sw[i+1:], "0123456789,"), ",") {
				w.Tabs = append(w.Tabs, interp.Int(t))
			}
		}
	}
	f := strings.Fields(rest)
	var pos []int
	for len(f) > 0 && len(pos) < 4 {
		v, ok := interp.ParseNumber(f[0])
		if !ok {
			break
		}
		pos = append(pos, int(v))
		f = f[1:]
	}
	for i, v := range pos {
		if v == -1 && !created {
			continue
		}
		switch i {
		case 0:
			w.X = v
		case 1:
			w.Y = v
		case 2:
			w.W = v
		case 3:
			w.H = v
		}
	}
	if len(f) > 0 && strings.HasPrefix(f[0], "/") {
		w.Command, f = f[0][1:], f[1:]
	}
	if len(f) > 0 {
		w.Popup, f = f[0], f[1:]
	}
	if len(f) > 0 {
		w.Font = f[0]
		if len(f) > 1 {
			w.FontSize = interp.Int(f[1])
		}
	}
	if w.Picture && w.Pic == nil {
		width, height := w.W, w.H
		if width <= 0 {
			width = 640
		}
		if height <= 0 {
			height = 480
		}
		w.Pic = newCanvas(width, height)
	}
	if created {
		u.windows = append(u.windows, w)
		u.Front.WindowOpened(w)
	} else {
		u.Front.WindowChanged(w)
	}
	return nil
}

// closeWindow removes a window and fires on CLOSE for @windows.
func (u *UI) closeWindow(w *Window) {
	for i, x := range u.windows {
		if x == w {
			u.windows = append(u.windows[:i], u.windows[i+1:]...)
			break
		}
	}
	if strings.EqualFold(u.active, w.Name) {
		u.active = u.lactive
	}
	ev := &interp.Event{Name: "CLOSE", Values: map[string]string{"target": w.Name}}
	u.rt.Dispatch(interp.Fire{Kind: "on", Name: "CLOSE", Event: ev,
		Match: func(f *interp.Frame, e *script.Event) (bool, error) {
			return len(e.Fields) > 0 && (e.Fields[0] == "@" || e.Fields[0] == "*"), nil
		}})
	u.Front.WindowClosed(w)
}

// CloseWindows closes custom windows for /close -@ [names].
func (u *UI) CloseWindows(_ *interp.CmdCall, names string) error {
	for _, w := range append([]*Window(nil), u.windows...) {
		match := strings.TrimSpace(names) == ""
		for _, n := range strings.Fields(names) {
			if interp.WildMatch(n, w.Name, false) {
				match = true
			}
		}
		if match {
			u.closeWindow(w)
		}
	}
	return nil
}

// cmdLine implements /aline /cline /dline /iline /rline /sline [-switches] [c] <@name> ...
func (u *UI) cmdLine(c *interp.CmdCall, name string) error {
	sw, rest := c.Switches()
	color := -1
	if first, after, _ := strings.Cut(strings.TrimSpace(rest), " "); !strings.HasPrefix(first, "@") && !strings.HasPrefix(first, "#") {
		if v, ok := interp.ParseNumber(first); ok {
			color, rest = int(v), after
		}
	}
	wname, args, _ := strings.Cut(strings.TrimSpace(rest), " ")
	w := u.window(wname)
	if w == nil {
		return c.Errorf("'%s' no such window", wname)
	}
	side := strings.Contains(sw, "l")
	lines := &w.Lines
	if side {
		lines = &w.Side
	}
	numArg := func() (int, string) {
		n, t, _ := strings.Cut(strings.TrimSpace(args), " ")
		return interp.Int(n), t
	}
	clearSel := func() {
		for i := range *lines {
			(*lines)[i].Selected = false
		}
	}
	switch name {
	case "aline":
		if strings.Contains(sw, "n") {
			for _, l := range *lines {
				if l.Text == args {
					return nil
				}
			}
		}
		if strings.Contains(sw, "s") {
			clearSel()
		}
		u.addLine(w, Line{Text: args, Color: color, Selected: strings.ContainsAny(sw, "sa")}, side)
		return nil
	case "iline":
		n, t := numArg()
		n = min(max(n, 1), len(*lines)+1)
		*lines = append((*lines)[:n-1], append([]Line{{Text: t, Color: color}}, (*lines)[n-1:]...)...)
	case "rline":
		n, t := numArg()
		if n >= 1 && n <= len(*lines) {
			(*lines)[n-1] = Line{Text: t, Color: color}
		}
	case "dline":
		a, b, _ := strings.Cut(strings.TrimSpace(args), "-")
		from, to := interp.Int(a), interp.Int(a)
		if b != "" {
			to = interp.Int(b)
		}
		from, to = max(from, 1), min(to, len(*lines))
		if from <= to {
			*lines = append((*lines)[:from-1], (*lines)[to:]...)
		}
	case "cline":
		n, _ := numArg()
		if n >= 1 && n <= len(*lines) {
			(*lines)[n-1].Color = color
		}
	case "sline":
		n, _ := numArg()
		if !strings.Contains(sw, "a") && !strings.Contains(sw, "r") {
			clearSel()
		}
		if n >= 1 && n <= len(*lines) {
			(*lines)[n-1].Selected = !strings.Contains(sw, "r")
		}
	}
	u.Front.WindowChanged(w)
	return nil
}

// Lines and SetLines let /filter read and write windows.
func (u *UI) Lines(_ *interp.Frame, name string) ([]string, bool) {
	w := u.window(name)
	if w == nil {
		return nil, false
	}
	out := make([]string, len(w.Lines))
	for i, l := range w.Lines {
		out[i] = l.Text
	}
	return out, true
}

func (u *UI) SetLines(_ *interp.Frame, name string, lines []string, clear bool) bool {
	w := u.window(name)
	if w == nil {
		return false
	}
	if clear {
		w.Lines = nil
	}
	for _, l := range lines {
		w.Lines = append(w.Lines, Line{Text: l, Color: -1})
	}
	u.Front.WindowChanged(w)
	return true
}

// cmdLoadbuf implements /loadbuf [lines] [-switches] <@window> <file>.
func (u *UI) cmdLoadbuf(c *interp.CmdCall) error {
	text := strings.TrimSpace(c.Args)
	count := -1
	if first, rest, _ := strings.Cut(text, " "); first != "" {
		if v, ok := interp.ParseNumber(first); ok {
			count, text = int(v), rest
		}
	}
	_, text = interp.SplitSwitches(text)
	name, file, _ := strings.Cut(strings.TrimSpace(text), " ")
	w := u.window(name)
	if w == nil {
		return c.Errorf("'%s' no such window", name)
	}
	path := strings.Trim(file, `" `)
	if st := storage.From(u.rt); st != nil {
		path = st.Path(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c.Errorf("unable to open '%s'", file)
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), "\n")
	if count >= 0 && count < len(lines) {
		lines = lines[len(lines)-count:]
	}
	for _, l := range lines {
		w.Lines = append(w.Lines, Line{Text: l, Color: -1})
	}
	u.Front.WindowChanged(w)
	return nil
}

// cmdSavebuf implements /savebuf [-a] [lines|N-M] <@window> <file>.
func (u *UI) cmdSavebuf(c *interp.CmdCall) error {
	sw, text := c.Switches()
	f := strings.Fields(text)
	from, to := 1, -1
	if len(f) > 2 {
		a, b, isRange := strings.Cut(f[0], "-")
		if isRange {
			from, to = interp.Int(a), interp.Int(b)
		} else {
			from = -interp.Int(a)
		}
		f = f[1:]
	}
	if len(f) < 2 {
		return c.InvalidParams()
	}
	w := u.window(f[0])
	if w == nil {
		return c.Errorf("'%s' no such window", f[0])
	}
	lines := w.Lines
	switch {
	case from < 0:
		lines = lines[max(0, len(lines)+from):]
	case to > 0:
		lines = lines[min(from-1, len(lines)):min(to, len(lines))]
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text + "\r\n")
	}
	path := strings.Trim(strings.Join(f[1:], " "), `"`)
	if st := storage.From(u.rt); st != nil {
		path = st.Path(path)
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if strings.Contains(sw, "a") {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	fh, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return c.Errorf("unable to open '%s'", path)
	}
	defer fh.Close()
	_, err = fh.WriteString(b.String())
	return err
}

// cmdFont implements /font [-asbiz|window] <size> <name> for custom windows.
func (u *UI) cmdFont(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	f := strings.Fields(rest)
	target := u.active
	if len(f) > 0 && strings.HasPrefix(f[0], "@") {
		target, f = f[0], f[1:]
	}
	w := u.window(target)
	if w == nil || len(f) < 2 {
		return nil
	}
	w.FontSize, w.Font, w.Bold = interp.Int(f[0]), strings.Join(f[1:], " "), strings.Contains(sw, "b")
	u.Front.WindowChanged(w)
	return nil
}

// identWindow implements $window(N|@name|@wild,N).prop.
func (u *UI) identWindow(c *interp.Call) (string, error) {
	arg := c.Arg(0)
	var w *Window
	if n, ok := interp.ParseNumber(arg); ok {
		i := int(n)
		switch {
		case i == 0:
			return strconv.Itoa(len(u.windows)), nil
		case i < 0:
			if strings.EqualFold(c.Prop, "w") {
				return "1920", nil
			}
			if strings.EqualFold(c.Prop, "h") {
				return "1080", nil
			}
			return "", nil
		case i <= len(u.windows):
			w = u.windows[i-1]
		}
	} else if strings.ContainsAny(arg, "*?") {
		var matches []*Window
		for _, x := range u.windows {
			if interp.WildMatch(arg, x.Name, false) {
				matches = append(matches, x)
			}
		}
		n := interp.Int(c.Arg(1))
		if len(c.Args) > 1 && n == 0 {
			return strconv.Itoa(len(matches)), nil
		}
		if n < 1 {
			n = 1
		}
		if n <= len(matches) {
			w = matches[n-1]
		}
	} else {
		w = u.window(arg)
	}
	if w == nil {
		return "", nil
	}
	num := strconv.Itoa
	switch strings.ToLower(c.Prop) {
	case "x", "cx", "dx":
		return num(w.X), nil
	case "y", "cy", "dy":
		return num(w.Y), nil
	case "w", "dw":
		return num(w.W), nil
	case "h", "dh":
		return num(w.H), nil
	case "bw":
		if w.Pic != nil {
			return num(w.Pic.Bounds().Dx()), nil
		}
		return "0", nil
	case "bh":
		if w.Pic != nil {
			return num(w.Pic.Bounds().Dy()), nil
		}
		return "0", nil
	case "title":
		return w.Title, nil
	case "fulltitle":
		return strings.TrimSpace(w.Name + " " + w.Title), nil
	case "state":
		return w.State, nil
	case "font":
		return w.Font, nil
	case "fontsize", "fontdialogsize":
		return num(w.FontSize), nil
	case "fontbold":
		return interp.Bool(w.Bold), nil
	case "fontitalic":
		return interp.Bool(false), nil
	case "ontop":
		return interp.Bool(w.OnTop), nil
	case "type":
		if w.Picture {
			return "picture", nil
		}
		return "custom", nil
	case "anysc":
		return interp.Bool(w.AnyConn), nil
	case "wid":
		return num(w.Wid), nil
	case "cid":
		return num(w.Cid), nil
	case "mdi":
		return interp.Bool(!w.Desktop), nil
	case "lb":
		switch {
		case w.Listbox && w.SideW > 0:
			return "2", nil
		case w.Listbox:
			return "1", nil
		}
		return "0", nil
	case "pbstate":
		return num(w.Progress[0]), nil
	case "pbpercent":
		return num(w.Progress[1]), nil
	case "hwnd", "idle", "sbcolor", "sbstate", "tbstate", "logfile", "stamp", "icon", "fontcs":
		return "", nil
	}
	return w.Name, nil
}

// identLine implements $line(@name,N,T).state|color.
func (u *UI) identLine(c *interp.Call) (string, error) {
	w := u.window(c.Arg(0))
	if w == nil {
		return "", nil
	}
	lines := w.Lines
	if c.Arg(2) == "1" {
		lines = w.Side
	}
	n := interp.Int(c.Arg(1))
	if n == 0 {
		return strconv.Itoa(len(lines)), nil
	}
	if n < 1 || n > len(lines) {
		return "", nil
	}
	l := lines[n-1]
	switch strings.ToLower(c.Prop) {
	case "state":
		if l.Selected {
			return "1", nil
		}
		return "0", nil
	case "color":
		return strconv.Itoa(l.Color), nil
	}
	return l.Text, nil
}

// identFline implements $fline(@name,text,N,T,S).text.
func (u *UI) identFline(c *interp.Call) (string, error) {
	w := u.window(c.Arg(0))
	if w == nil {
		return "", nil
	}
	t := interp.Int(c.Arg(3))
	lines := w.Lines
	if t&1 != 0 {
		lines = w.Side
	}
	n := interp.Int(c.Arg(2))
	start := max(interp.Int(c.Arg(4)), 1)
	count := 0
	for i := start; i <= len(lines); i++ {
		var ok bool
		if t&2 != 0 {
			re, err := regex.Compile(c.Arg(1))
			if err != nil {
				return "", nil
			}
			ok, _ = re.MatchString(lines[i-1].Text)
		} else {
			ok = interp.WildMatch(c.Arg(1), lines[i-1].Text, false)
		}
		if !ok {
			continue
		}
		count++
		if count == n {
			u.flinen = i
			if strings.EqualFold(c.Prop, "text") {
				return lines[i-1].Text, nil
			}
			return strconv.Itoa(i), nil
		}
	}
	if n == 0 {
		return strconv.Itoa(count), nil
	}
	return "", nil
}

// identSline implements $sline(@name,N).ln.
func (u *UI) identSline(c *interp.Call) (string, error) {
	w := u.window(c.Arg(0))
	if w == nil {
		return "", nil
	}
	var sel []int
	for i, l := range w.Lines {
		if l.Selected {
			sel = append(sel, i+1)
		}
	}
	n := interp.Int(c.Arg(1))
	if n == 0 {
		return strconv.Itoa(len(sel)), nil
	}
	if n < 1 || n > len(sel) {
		return "", nil
	}
	if strings.EqualFold(c.Prop, "ln") {
		return strconv.Itoa(sel[n-1]), nil
	}
	return w.Lines[sel[n-1]-1].Text, nil
}

// identMouse implements $mouse.x|y|key|lb|dx|dy|cx|cy|win|mx|my.
func (u *UI) identMouse(c *interp.Call) (string, error) {
	m := u.mouse
	switch strings.ToLower(c.Prop) {
	case "x", "mx", "cx":
		return strconv.Itoa(m.X), nil
	case "y", "my", "cy":
		return strconv.Itoa(m.Y), nil
	case "dx":
		return strconv.Itoa(m.DX), nil
	case "dy":
		return strconv.Itoa(m.DY), nil
	case "key":
		return strconv.Itoa(m.Key), nil
	case "lb":
		return strconv.Itoa(m.Lb), nil
	case "win":
		return m.Window, nil
	}
	return strconv.Itoa(m.X) + " " + strconv.Itoa(m.Y), nil
}

// MenuItem is one entry of an @window popup menu.
type MenuItem struct {
	Label   string
	Command string
	Depth   int
}

// menuLines returns the menu definition lines for a window, from scripts
// with menu blocks whose targets include the window's popup name.
func (u *UI) menuLines(w *Window) []struct {
	text string
	file *script.File
} {
	var out []struct {
		text string
		file *script.File
	}
	for _, f := range u.rt.Files() {
		for _, m := range f.Menus {
			if m.Group != nil && !m.Group.On {
				continue
			}
			for _, t := range m.Targets {
				if strings.EqualFold(t, w.Popup) || strings.EqualFold(t, w.Name) || t == "@" {
					for _, l := range m.Lines {
						out = append(out, struct {
							text string
							file *script.File
						}{l, f})
					}
				}
			}
		}
	}
	return out
}

// mouseEvents are the special menu lines that handle window mouse events.
var mouseEvents = map[string]bool{
	"sclick": true, "dclick": true, "uclick": true, "rclick": true, "lbclick": true,
	"mouse": true, "leave": true, "drop": true, "wheel": true,
}

// MouseEvent handles a mouse event in a custom window: it runs the matching
// "event:" line of the window's menu definition.
func (u *UI) MouseEvent(name, kind string, m Mouse) {
	w := u.window(name)
	if w == nil {
		return
	}
	m.Window = name
	u.mouse = m
	if kind == "sclick" {
		key := strings.ToLower(name)
		u.clicks[key] = append(u.clicks[key], image.Pt(m.X, m.Y))
	}
	for _, ml := range u.menuLines(w) {
		label, cmd, ok := strings.Cut(ml.text, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(label), kind) {
			continue
		}
		ev := &interp.Event{Name: "MENU", Values: map[string]string{"target": name, "menu": name, "menutype": "@"}}
		f := u.rt.NewFrame(ml.file, ev)
		f.Cid, f.Caller = w.Cid, "mouse"
		if w.Listbox {
			for i, l := range w.Lines {
				if l.Selected {
					f.Params = []string{strconv.Itoa(i + 1)}
					break
				}
			}
		}
		u.runCommands(f, cmd)
		return
	}
}

// runCommands runs menu command text as a block, so | and { } work as in scripts.
func (u *UI) runCommands(f *interp.Frame, text string) {
	block, errs := script.ParseCommands(text)
	_ = u.rt.Run(func() error {
		if len(errs) > 0 {
			return interp.Errorf("", "%v", errs[0])
		}
		err := f.RunBody(block)
		if interp.IsHalt(err) {
			return nil
		}
		return err
	})
}

// Menu evaluates a window's popup menu for display: labels are evaluated,
// $submenu entries expanded, and mouse event lines skipped.
func (u *UI) Menu(name string) []MenuItem {
	w := u.window(name)
	if w == nil {
		return nil
	}
	var items []MenuItem
	for _, ml := range u.menuLines(w) {
		depth := len(ml.text) - len(strings.TrimLeft(ml.text, "."))
		text := strings.TrimLeft(ml.text, ".")
		label, cmd, _ := strings.Cut(text, ":")
		if mouseEvents[strings.ToLower(strings.TrimSpace(label))] {
			continue
		}
		ev := &interp.Event{Name: "MENU", Values: map[string]string{"target": name, "menu": name, "menutype": "@"}}
		f := u.rt.NewFrame(ml.file, ev)
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(label)), "$submenu(") {
			items = append(items, u.expandSubmenu(f, label, depth)...)
			continue
		}
		v, err := f.Eval(label)
		if err != nil || v == "" {
			continue
		}
		items = append(items, MenuItem{Label: v, Command: cmd, Depth: depth})
	}
	return items
}

// expandSubmenu evaluates $submenu($alias($1)) for $1 = 1, 2, ... until $null.
func (u *UI) expandSubmenu(f *interp.Frame, label string, depth int) []MenuItem {
	inner := strings.TrimSpace(label)
	inner = inner[len("$submenu(") : len(inner)-1]
	var out []MenuItem
	for i := 1; i < 1000; i++ {
		f.Params = []string{strconv.Itoa(i)}
		v, err := f.Eval(inner)
		if err != nil || v == "" {
			break
		}
		if v == "-" {
			out = append(out, MenuItem{Label: "-", Depth: depth})
			continue
		}
		l, cmd, _ := strings.Cut(v, ":")
		out = append(out, MenuItem{Label: l, Command: cmd, Depth: depth})
	}
	return out
}

// RunMenuItem runs a chosen popup menu command.
func (u *UI) RunMenuItem(name string, item MenuItem) {
	w := u.window(name)
	ev := &interp.Event{Name: "MENU", Values: map[string]string{"target": name, "menu": name, "menutype": "@"}}
	f := u.rt.NewFrame(nil, ev)
	if w != nil {
		f.Cid = w.Cid
	}
	f.Caller = "menu"
	u.runCommands(f, item.Command)
}

func identSubmenu(c *interp.Call) (string, error) { return "", nil }

// identStyle implements $style(N) in menu items: 1 checked, 2 disabled, 3 both.
func identStyle(c *interp.Call) (string, error) {
	switch interp.Int(c.Arg(0)) {
	case 1:
		return "\x01", nil
	case 2:
		return "\x02", nil
	case 3:
		return "\x03", nil
	}
	return "", nil
}

// Input handles a line typed in a window's editbox: on INPUT fires, then
// the window's default command runs unless halted.
func (u *UI) Input(name, text string) {
	w := u.window(name)
	if w == nil {
		return
	}
	ev := &interp.Event{Name: "INPUT", Values: map[string]string{"target": name}}
	halted := u.rt.Dispatch(interp.Fire{Kind: "on", Name: "INPUT", Event: ev, Params: strings.Fields(text), Cid: w.Cid,
		Match: func(f *interp.Frame, e *script.Event) (bool, error) {
			if len(e.Fields) == 0 {
				return true, nil
			}
			return e.Fields[0] == "@" || e.Fields[0] == "*" || interp.WildMatch(e.Fields[0], name, false), nil
		}})
	if halted || w.Command == "" {
		return
	}
	f := u.rt.NewFrame(nil, ev)
	f.Cid = w.Cid
	_ = u.rt.Run(func() error { return f.ExecLine(w.Command+" "+text, 0) })
}

// Key handles KEYDOWN, KEYUP and CHAR events in a custom window.
func (u *UI) Key(name, kind string, key int, char rune, repeat bool) {
	values := map[string]string{
		"target": name, "keyval": strconv.Itoa(key), "keyrpt": interp.Bool(repeat),
	}
	if char != 0 && utf8.ValidRune(char) {
		values["keychar"] = string(char)
	}
	ev := &interp.Event{Name: kind, Values: values}
	u.rt.Dispatch(interp.Fire{Kind: "on", Name: kind, Event: ev,
		Match: func(f *interp.Frame, e *script.Event) (bool, error) {
			if len(e.Fields) < 2 || !interp.WildMatch(e.Fields[0], name, false) && e.Fields[0] != "@" && e.Fields[0] != "*" {
				return false, nil
			}
			if e.Fields[1] == "*" {
				return true, nil
			}
			for _, k := range strings.Split(e.Fields[1], ",") {
				if interp.Int(k) == key {
					return true, nil
				}
			}
			return false, nil
		}})
}

// Activate handles focus moving to a window or dialog and fires on ACTIVE.
func (u *UI) Activate(name string) {
	if strings.EqualFold(u.active, name) {
		return
	}
	u.setActive(name)
	ev := &interp.Event{Name: "ACTIVE", Values: map[string]string{"target": name}}
	loc := "*"
	switch {
	case strings.HasPrefix(name, "@"):
		loc = "@"
	case strings.HasPrefix(name, "="):
		loc = "="
	}
	u.rt.Dispatch(interp.Fire{Kind: "on", Name: "ACTIVE", Event: ev,
		Match: func(f *interp.Frame, e *script.Event) (bool, error) {
			return len(e.Fields) == 0 || e.Fields[0] == "*" || strings.Contains(e.Fields[0], loc), nil
		}})
}

// AppActive handles the runtime's windows gaining or losing focus.
func (u *UI) AppActive(on bool) {
	if u.appActive == on {
		return
	}
	u.appActive = on
	u.rt.Dispatch(interp.Fire{Kind: "on", Name: "APPACTIVE", Event: &interp.Event{Name: "APPACTIVE", Values: map[string]string{}}})
}

// CloseWindow closes a custom window when its user closes it.
func (u *UI) CloseWindow(name string) {
	if w := u.window(name); w != nil {
		u.closeWindow(w)
	}
}

// SelectLine selects listbox line n (1-based) of a window, as a user click
// does, and runs the window's lbclick menu event.
func (u *UI) SelectLine(name string, n int, add bool) {
	w := u.window(name)
	if w == nil || n < 1 || n > len(w.Lines) {
		return
	}
	if !add {
		for i := range w.Lines {
			w.Lines[i].Selected = false
		}
	}
	w.Lines[n-1].Selected = true
	u.Front.WindowChanged(w)
	u.MouseEvent(name, "lbclick", Mouse{Lb: 1})
}

// EditboxChanged records a user's edit of a window's editbox and fires on
// KEYDOWN for the last typed character, so scripts can react to typing.
func (u *UI) EditboxChanged(name, text string) {
	w := u.window(name)
	if w == nil || w.EditText == text {
		return
	}
	old := w.EditText
	w.EditText = text
	if text == "" {
		u.Key(name, "KEYDOWN", 8, 0, false)
		return
	}
	r := []rune(text)
	last := r[len(r)-1]
	if len(text) < len(old) {
		u.Key(name, "KEYDOWN", 8, 0, false)
		return
	}
	code := int(last)
	if last >= 'a' && last <= 'z' {
		code -= 'a' - 'A'
	}
	u.Key(name, "KEYDOWN", code, last, false)
}

// identEditbox implements $editbox(@window) for custom windows.
func (u *UI) identEditbox(c *interp.Call) (string, error) {
	name := c.Arg(0)
	if !c.Paren {
		name = u.active
	}
	if w := u.window(name); w != nil {
		return w.EditText, nil
	}
	return "", nil
}

// cmdEditbox implements /editbox [-af] [@window] <text> for custom windows:
// it replaces (or with -a appends to) the editbox text.
func (u *UI) cmdEditbox(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	name := u.active
	if first, after, _ := strings.Cut(rest, " "); strings.HasPrefix(first, "@") {
		name, rest = first, after
	}
	w := u.window(name)
	if w == nil {
		return nil
	}
	if strings.Contains(sw, "a") {
		w.EditText += rest
	} else {
		w.EditText = rest
	}
	w.EditSeq++
	u.Front.WindowChanged(w)
	return nil
}
