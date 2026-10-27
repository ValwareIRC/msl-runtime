package ui

import (
	"image"
	"sort"
	"strconv"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/regex"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// Frontend renders the model. Every method is called on the runtime
// goroutine with the model already updated; implementations copy what they
// need and draw on their own thread.
type Frontend interface {
	// Interactive reports whether a person can use the windows (false when headless).
	Interactive() bool
	DialogOpened(d *Dialog)
	DialogChanged(d *Dialog)
	DialogClosed(d *Dialog)
	WindowOpened(w *Window)
	WindowChanged(w *Window)
	WindowClosed(w *Window)
	// Prompt shows a script-invoked prompt ($input, $sfile, $msfile, $sdir)
	// and calls done with the answer on the runtime goroutine.
	Prompt(p Prompt, done func(answer []string, ok bool))
}

// Prompt describes a modal prompt a script asked for.
type Prompt struct {
	Kind    string // input, file, files, dir
	Text    string
	Title   string
	Default string
	Options string // $input options
	Dir     string
	OkText  string
}

// Headless is a Frontend with no display.
type Headless struct{}

func (Headless) Interactive() bool                          { return false }
func (Headless) DialogOpened(*Dialog)                       {}
func (Headless) DialogChanged(*Dialog)                      {}
func (Headless) DialogClosed(*Dialog)                       {}
func (Headless) WindowOpened(*Window)                       {}
func (Headless) WindowChanged(*Window)                      {}
func (Headless) WindowClosed(*Window)                       {}
func (Headless) Prompt(_ Prompt, done func([]string, bool)) { done(nil, false) }

// UI is the per-runtime interface model.
type UI struct {
	rt        *interp.Runtime
	Front     Frontend
	dialogs   []*Dialog
	windows   []*Window
	active    string
	lactive   string
	appActive bool
	sfstate   string
	msfiles   []string
	mouse     Mouse
	nextWid   int
	flinen    int
	clicks    map[string][]image.Point
}

type uiKey struct{}

// Install registers dialogs and windows on rt with a headless frontend.
func Install(rt *interp.Runtime) *UI {
	u := &UI{rt: rt, Front: Headless{}, active: interp.StatusWindow, nextWid: 100, clicks: map[string][]image.Point{}}
	interp.Ext(rt, uiKey{}, func() *UI { return u })
	u.installDialogs()
	u.installWindows()
	u.installPrompts()
	u.installDraw()
	rt.DeclareEvents("DIALOG", "ACTIVE", "APPACTIVE", "KEYDOWN", "KEYUP", "CHAR", "INPUT")
	return u
}

// From returns the UI installed on rt.
func From(rt *interp.Runtime) *UI { return interp.Ext(rt, uiKey{}, func() *UI { return nil }) }

// Dialogs lists open dialogs.
func (u *UI) Dialogs() []*Dialog { return u.dialogs }

// Dialog returns an open dialog by name.
func (u *UI) Dialog(name string) *Dialog {
	for _, d := range u.dialogs {
		if strings.EqualFold(d.Name, name) {
			return d
		}
	}
	return nil
}

// findTable finds a dialog table visible from file f.
func (u *UI) findTable(f *script.File, name string) *script.Dialog {
	for _, file := range u.rt.Files() {
		for _, d := range file.Dialogs {
			if strings.EqualFold(d.Name, name) && (!d.Local || file == f) {
				return d
			}
		}
	}
	return nil
}

// fireDialog runs on DIALOG for a dialog event and reports whether it was halted.
func (u *UI) fireDialog(d *Dialog, event string, id int) bool {
	ev := &interp.Event{Name: "DIALOG", Values: map[string]string{
		"dname": d.Name, "devent": event, "did": strconv.Itoa(id),
	}}
	return u.rt.Dispatch(interp.Fire{Kind: "on", Name: "DIALOG", Event: ev, Cid: d.Cid,
		Match: func(f *interp.Frame, e *script.Event) (bool, error) {
			if len(e.Fields) < 3 {
				return false, nil
			}
			return interp.WildMatch(e.Fields[0], d.Name, false) &&
				(e.Fields[1] == "*" || strings.EqualFold(e.Fields[1], event)) &&
				matchIDs(e.Fields[2], id), nil
		}})
}

// open creates and shows a dialog, firing its init event.
func (u *UI) open(f *interp.Frame, name, table string, modal bool) (*Dialog, error) {
	def := u.findTable(f.File, table)
	if def == nil {
		return nil, interp.Errorf("dialog", "no such table '%s'", table)
	}
	d, err := buildDialog(f, name, def)
	if err != nil {
		return nil, err
	}
	d.Modal, d.Cid = modal, f.Cid
	u.dialogs = append(u.dialogs, d)
	u.fireDialog(d, "init", 0)
	if d.closed {
		return d, nil
	}
	u.Front.DialogOpened(d)
	return d, nil
}

// close closes a dialog, firing close (unless quiet) and setting %var bindings.
func (u *UI) close(d *Dialog, quiet bool) {
	if d.closed {
		return
	}
	d.closed = true
	if !quiet {
		u.fireDialog(d, "close", 0)
	}
	for _, c := range d.Controls {
		if c.Var != "" {
			u.rt.Globals.Set(c.Var, c.Value())
		}
	}
	for i, x := range u.dialogs {
		if x == d {
			u.dialogs = append(u.dialogs[:i], u.dialogs[i+1:]...)
			break
		}
	}
	u.Front.DialogClosed(d)
}

// changed tells the frontend a dialog was modified by a script.
func (u *UI) changed(d *Dialog) {
	if !d.closed {
		u.Front.DialogChanged(d)
	}
}

// Click handles a user click on a control (called by the frontend via Post).
func (u *UI) Click(name string, id int) {
	d := u.Dialog(name)
	if d == nil {
		return
	}
	c := d.Control(id)
	if c == nil || !c.Enabled {
		return
	}
	switch c.Kind {
	case "check":
		if c.Has("3state") {
			c.State = (c.State + 1) % 3
		} else {
			c.State = 1 - c.State
		}
	case "radio":
		u.selectRadio(d, c)
	case "tab":
		for _, t := range d.tabs() {
			t.State = 0
		}
		c.State = 1
	}
	event := "sclick"
	if c.Kind == "item" {
		event = "menu"
	}
	halted := u.fireDialog(d, event, id)
	switch {
	case id == d.Ok && !halted: // /halt in the ok button's sclick keeps the dialog open
		d.okHit = true
		u.close(d, false)
	case id == d.Cancel:
		u.close(d, false)
	}
}

// selectRadio checks a radio and unchecks the others in its group.
func (u *UI) selectRadio(d *Dialog, c *Control) {
	start, end := 0, len(d.Controls)
	for i, x := range d.Controls {
		if x == c {
			for j := i; j >= 0; j-- {
				if d.Controls[j].Kind != "radio" || d.Controls[j].Has("group") {
					start = j
					break
				}
			}
			for j := i + 1; j < len(d.Controls); j++ {
				if d.Controls[j].Kind != "radio" || d.Controls[j].Has("group") {
					end = j
					break
				}
			}
		}
	}
	for _, x := range d.Controls[start:end] {
		if x.Kind == "radio" {
			x.State = 0
		}
	}
	c.State = 1
}

// SetText handles user edits in an edit or combo control.
func (u *UI) SetText(name string, id int, text string) {
	d := u.Dialog(name)
	if d == nil {
		return
	}
	if c := d.Control(id); c != nil {
		c.Text, c.Edited = text, true
		if c.Has("multi") {
			c.Lines = strings.Split(text, "\n")
		}
		u.fireDialog(d, "edit", id)
	}
}

// SelectLines handles user selection in a list or combo; double reports a double click.
func (u *UI) SelectLines(name string, id int, lines []int, double bool) {
	d := u.Dialog(name)
	if d == nil {
		return
	}
	if c := d.Control(id); c != nil {
		c.Sel = lines
		event := "sclick"
		if double {
			event = "dclick"
		}
		u.fireDialog(d, event, id)
	}
}

// SetScroll handles user scrolling of a scroll control.
func (u *UI) SetScroll(name string, id, pos int) {
	d := u.Dialog(name)
	if d == nil {
		return
	}
	if c := d.Control(id); c != nil {
		c.Pos = pos
		u.fireDialog(d, "scroll", id)
	}
}

// CloseDialog handles the user closing a dialog's window (as cancel).
func (u *UI) CloseDialog(name string) {
	if d := u.Dialog(name); d != nil {
		u.close(d, false)
	}
}

func (u *UI) installDialogs() {
	rt := u.rt
	rt.RegisterCommand("dialog", interp.Command{Fn: u.cmdDialog})
	rt.RegisterCommand("did", interp.Command{Fn: u.cmdDid})
	rt.RegisterCommand("didtok", interp.Command{Fn: u.cmdDidtok})
	rt.RegisterIdent("dialog", u.identDialog)
	rt.RegisterIdent("did", u.identDid)
	rt.RegisterIdent("didwm", func(c *interp.Call) (string, error) { return u.identDidFind(c, false) })
	rt.RegisterIdent("didreg", func(c *interp.Call) (string, error) { return u.identDidFind(c, true) })
	rt.RegisterIdent("didtok", u.identDidtok)
	rt.RegisterIdent("dname", func(c *interp.Call) (string, error) { v, _ := c.F.Event.Get("dname"); return v, nil })
	rt.RegisterIdent("devent", func(c *interp.Call) (string, error) { v, _ := c.F.Event.Get("devent"); return v, nil })
	rt.RegisterIdent("dbuw", func(*interp.Call) (string, error) { return "1.75", nil })
	rt.RegisterIdent("dbuh", func(*interp.Call) (string, error) { return "1.625", nil })
}

// cmdDialog implements /dialog -mdtsonkcvieagxhr <name> [table] [x y w h] [text].
func (u *UI) cmdDialog(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	name, rest, _ := strings.Cut(strings.TrimSpace(rest), " ")
	if name == "" {
		return c.InvalidParams()
	}
	if strings.Contains(sw, "m") {
		if u.Dialog(name) != nil {
			return c.Errorf("'%s' name in use", name)
		}
		table := strings.Fields(rest)
		t := name
		if len(table) > 0 {
			t = table[0]
		}
		_, err := u.open(c.F, name, t, false)
		return err
	}
	d := u.Dialog(name)
	if d == nil {
		return c.Errorf("'%s' no such dialog", name)
	}
	switch {
	case strings.Contains(sw, "x"):
		u.close(d, true)
		return nil
	case strings.Contains(sw, "k"):
		d.okHit = true
		u.close(d, false)
		return nil
	case strings.Contains(sw, "c"):
		u.close(d, false)
		return nil
	case strings.Contains(sw, "g"):
		d.Name = strings.TrimSpace(rest)
	case strings.Contains(sw, "t"):
		d.Title = rest
	case strings.Contains(sw, "s"):
		v := ints(rest)
		d.X, d.Y, d.W, d.H = v[0], v[1], v[2], v[3]
	case strings.Contains(sw, "r"):
		d.X, d.Y = -1, -1
	case strings.Contains(sw, "o"):
		d.OnTop = true
	case strings.Contains(sw, "n"):
		d.OnTop = false
	case strings.Contains(sw, "v"):
		u.setActive(d.Name)
	}
	u.changed(d)
	return nil
}

// targets resolves "name id[,id...]" for /did, with ids like 1,3-5.
func (u *UI) targets(c *interp.CmdCall, rest string) (*Dialog, []*Control, string, error) {
	f := strings.SplitN(strings.TrimSpace(rest), " ", 3)
	if len(f) < 2 {
		return nil, nil, "", c.InvalidParams()
	}
	d := u.Dialog(f[0])
	if d == nil {
		return nil, nil, "", c.Errorf("'%s' no such dialog", f[0])
	}
	var ctrls []*Control
	for _, id := range parseIDs(f[1]) {
		if ctl := d.Control(id); ctl != nil {
			ctrls = append(ctrls, ctl)
		}
	}
	tail := ""
	if len(f) == 3 {
		tail = f[2]
	}
	return d, ctrls, tail, nil
}

// cmdDid implements /did -ftebvhnmcukslradiogjz name id [n] [text].
func (u *UI) cmdDid(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	d, ctrls, tail, err := u.targets(c, rest)
	if err != nil {
		return err
	}
	// N is the first word of the tail when it is a number and the switch uses it.
	n, text := 0, tail
	if strings.ContainsAny(sw, "dioczkus") {
		if first, after, _ := strings.Cut(tail, " "); first != "" {
			if v, ok := interp.ParseNumber(first); ok {
				n, text = int(v), after
				if strings.Contains(sw, "o") || strings.Contains(sw, "i") || strings.Contains(sw, "c") || strings.Contains(sw, "z") {
					text = after
				}
			}
		}
	}
	for _, ctl := range ctrls {
		applyDid(d, ctl, sw, n, text, tail)
	}
	u.changed(d)
	return nil
}

func applyDid(d *Dialog, c *Control, sw string, n int, text, tail string) {
	lines := func() []string {
		if c.Kind == "edit" && len(c.Lines) == 0 && c.Text != "" {
			c.Lines = []string{c.Text}
		}
		return c.Lines
	}
	for i := 0; i < len(sw); i++ {
		switch sw[i] {
		case 'f':
			d.Focus = c.ID
			if c.Kind == "tab" {
				for _, t := range d.tabs() {
					t.State = 0
				}
				c.State = 1
			}
		case 't':
			d.Ok = c.ID
		case 'e':
			c.Enabled = true
		case 'b':
			c.Enabled = false
		case 'v':
			c.Visible = true
		case 'h':
			c.Visible = false
		case 'n':
			delete(c.Styles, "read")
		case 'm':
			c.Styles["read"] = ""
		case 'r':
			c.Text, c.Lines, c.Sel, c.Checked = "", nil, nil, nil
		case 'a':
			if c.Kind == "menu" {
				continue
			}
			if c.Kind == "text" || c.Kind == "button" || (c.Kind == "edit" && !c.Has("multi")) {
				c.Text += tail
				continue
			}
			c.Lines = append(lines(), tail)
			c.Checked = append(c.Checked, false)
			if c.Has("sort") {
				sort.SliceStable(c.Lines, func(i, j int) bool { return strings.ToLower(c.Lines[i]) < strings.ToLower(c.Lines[j]) })
			}
		case 'd':
			if n >= 1 && n <= len(lines()) {
				c.Lines = append(c.Lines[:n-1], c.Lines[n:]...)
				if n-1 < len(c.Checked) {
					c.Checked = append(c.Checked[:n-1], c.Checked[n:]...)
				}
			}
		case 'i':
			l := lines()
			if n < 1 {
				n = 1
			}
			for len(l) < n-1 {
				l = append(l, "")
			}
			c.Lines = append(l[:n-1], append([]string{text}, l[n-1:]...)...)
		case 'o':
			if c.Kind == "text" || c.Kind == "button" || c.Kind == "check" || c.Kind == "radio" || (c.Kind == "edit" && n <= 1 && !c.Has("multi")) {
				c.Text = text
				continue
			}
			l := lines()
			for len(l) < n {
				l = append(l, "")
			}
			if n >= 1 {
				l[n-1] = text
			}
			c.Lines = l
		case 'c':
			switch c.Kind {
			case "check", "radio":
				if strings.Contains(sw, "u") {
					c.State = 2
				} else {
					c.State = 1
				}
			default:
				if n > 0 {
					if !strings.Contains(sw, "k") {
						c.Sel = nil
					}
					c.Sel = append(c.Sel, n)
				}
			}
		case 'u':
			switch c.Kind {
			case "check", "radio":
				if !strings.Contains(sw, "c") {
					c.State = 0
				}
			default:
				if n > 0 {
					kept := c.Sel[:0]
					for _, s := range c.Sel {
						if s != n {
							kept = append(kept, s)
						}
					}
					c.Sel = kept
				} else {
					c.Sel = nil
				}
			}
		case 's', 'l':
			for len(c.Checked) < len(c.Lines) {
				c.Checked = append(c.Checked, false)
			}
			if n >= 1 && n <= len(c.Checked) {
				c.Checked[n-1] = sw[i] == 's'
			}
		case 'j':
			c.Edited = false
		case 'g':
			c.File = tail
		case 'z':
			if c.Kind == "scroll" {
				v := ints(tail)
				if v[0] >= 0 {
					c.Min, c.Max = v[0], v[1]
				}
			}
		}
	}
}

// cmdDidtok implements /didtok name id C text.
func (u *UI) cmdDidtok(c *interp.CmdCall) error {
	f := strings.SplitN(strings.TrimSpace(c.Args), " ", 4)
	if len(f) < 4 {
		return c.InvalidParams()
	}
	d := u.Dialog(f[0])
	if d == nil {
		return c.Errorf("'%s' no such dialog", f[0])
	}
	ctl := d.Control(interp.Int(f[1]))
	if ctl == nil {
		return nil
	}
	ctl.Lines = append(ctl.Lines, interp.Tokens(f[3], rune(interp.Int(f[2])))...)
	if ctl.Has("sort") {
		sort.SliceStable(ctl.Lines, func(i, j int) bool { return strings.ToLower(ctl.Lines[i]) < strings.ToLower(ctl.Lines[j]) })
	}
	u.changed(d)
	return nil
}

// dialogFor resolves the dialog and id of $did(name,id) or $did(id) in an event.
func (u *UI) dialogFor(c *interp.Call) (*Dialog, *Control, []string) {
	args := c.Args
	var d *Dialog
	if len(args) > 0 {
		if _, ok := interp.ParseNumber(args[0]); ok && u.Dialog(args[0]) == nil {
			name, _ := c.F.Event.Get("dname")
			d = u.Dialog(name)
		} else {
			d = u.Dialog(args[0])
			args = args[1:]
		}
	}
	if d == nil || len(args) == 0 {
		return d, nil, args
	}
	return d, d.Control(interp.Int(args[0])), args[1:]
}

// identDid implements $did and $did(name,id,N).prop.
func (u *UI) identDid(c *interp.Call) (string, error) {
	if !c.Paren {
		v, _ := c.F.Event.Get("did")
		return v, nil
	}
	d, ctl, rest := u.dialogFor(c)
	if d == nil {
		return "", nil
	}
	prop := strings.ToLower(c.Prop)
	if ctl == nil {
		if prop == "isid" {
			return interp.Bool(false), nil
		}
		return "", nil
	}
	n := 0
	if len(rest) > 0 {
		n = interp.Int(rest[0])
	}
	line := func() string {
		if n > 0 {
			if n <= len(ctl.Lines) {
				return ctl.Lines[n-1]
			}
			return ""
		}
		if (ctl.Kind == "list" || ctl.Kind == "combo") && len(ctl.Sel) > 0 && ctl.Sel[0] <= len(ctl.Lines) && ctl.Kind == "list" {
			return ctl.Lines[ctl.Sel[0]-1]
		}
		if len(ctl.Lines) > 0 && ctl.Kind == "edit" {
			return ctl.Lines[0]
		}
		return ctl.Text
	}
	switch prop {
	case "", "text":
		return line(), nil
	case "len":
		return strconv.Itoa(len([]rune(line()))), nil
	case "lines":
		if len(ctl.Lines) == 0 && ctl.Text != "" {
			return "1", nil
		}
		return strconv.Itoa(len(ctl.Lines)), nil
	case "sel":
		if n == 0 {
			return strconv.Itoa(len(ctl.Sel)), nil
		}
		if n <= len(ctl.Sel) {
			return strconv.Itoa(ctl.Sel[n-1]), nil
		}
		return "", nil
	case "seltext":
		if len(ctl.Sel) > 0 && ctl.Sel[0] <= len(ctl.Lines) {
			return ctl.Lines[ctl.Sel[0]-1], nil
		}
		if ctl.SelEnd > ctl.SelStart {
			r := []rune(ctl.Text)
			return string(r[min(ctl.SelStart, len(r)):min(ctl.SelEnd, len(r))]), nil
		}
		return "", nil
	case "selstart":
		return strconv.Itoa(ctl.SelStart), nil
	case "selend":
		return strconv.Itoa(ctl.SelEnd), nil
	case "edited":
		return interp.Bool(ctl.Edited), nil
	case "state":
		if ctl.Kind == "scroll" {
			return strconv.Itoa(ctl.Pos), nil
		}
		return strconv.Itoa(ctl.State), nil
	case "visible":
		return interp.Bool(ctl.Visible), nil
	case "enabled":
		return interp.Bool(ctl.Enabled), nil
	case "isid":
		return interp.Bool(true), nil
	case "next", "prev":
		for i, x := range d.Controls {
			if x == ctl {
				j := i + 1
				if prop == "prev" {
					j = i - 1
				}
				if j >= 0 && j < len(d.Controls) {
					return strconv.Itoa(d.Controls[j].ID), nil
				}
			}
		}
		return "", nil
	case "csel":
		var checked []int
		for i, on := range ctl.Checked {
			if on {
				checked = append(checked, i+1)
			}
		}
		if n == 0 {
			return strconv.Itoa(len(checked)), nil
		}
		if n <= len(checked) {
			return strconv.Itoa(checked[n-1]), nil
		}
		return "", nil
	case "cstate":
		if n >= 1 && n <= len(ctl.Checked) && ctl.Checked[n-1] {
			return "1", nil
		}
		return "0", nil
	}
	return "", nil
}

// identDidFind implements $didwm and $didreg(name,id,text,N).
func (u *UI) identDidFind(c *interp.Call, isRegex bool) (string, error) {
	d, ctl, rest := u.dialogFor(c)
	if d == nil || ctl == nil || len(rest) == 0 {
		return "0", nil
	}
	start := 1
	if len(rest) > 1 {
		start = max(1, interp.Int(rest[1]))
	}
	for i := start; i <= len(ctl.Lines); i++ {
		var ok bool
		if isRegex {
			re, err := regex.Compile(rest[0])
			if err != nil {
				return "0", nil
			}
			ok, _ = re.MatchString(ctl.Lines[i-1])
		} else {
			ok = interp.WildMatch(rest[0], ctl.Lines[i-1], false)
		}
		if ok {
			return strconv.Itoa(i), nil
		}
	}
	return "0", nil
}

// identDidtok implements $didtok(name,id,C).
func (u *UI) identDidtok(c *interp.Call) (string, error) {
	d, ctl, rest := u.dialogFor(c)
	if d == nil || ctl == nil || len(rest) == 0 {
		return "", nil
	}
	return strings.Join(ctl.Lines, string(rune(interp.Int(rest[0])))), nil
}

// identDialog implements $dialog(name|N).prop and modal $dialog(name,table[,parent]).
func (u *UI) identDialog(c *interp.Call) (string, error) {
	if len(c.Args) >= 2 && c.Prop == "" {
		return u.modal(c)
	}
	var d *Dialog
	if n, ok := interp.ParseNumber(c.Arg(0)); ok {
		i := int(n)
		if i == 0 {
			return strconv.Itoa(len(u.dialogs)), nil
		}
		if i >= 1 && i <= len(u.dialogs) {
			d = u.dialogs[i-1]
		}
	} else {
		d = u.Dialog(c.Arg(0))
	}
	if d == nil {
		return "", nil
	}
	switch strings.ToLower(c.Prop) {
	case "x":
		return strconv.Itoa(d.X), nil
	case "y":
		return strconv.Itoa(d.Y), nil
	case "w", "cw":
		return strconv.Itoa(d.W), nil
	case "h", "ch":
		return strconv.Itoa(d.H), nil
	case "title":
		return d.Title, nil
	case "modal":
		return interp.Bool(d.Modal), nil
	case "table":
		return d.Table, nil
	case "ok":
		return strconv.Itoa(d.Ok), nil
	case "cancel":
		return strconv.Itoa(d.Cancel), nil
	case "result":
		return strconv.Itoa(d.Result), nil
	case "focus":
		return strconv.Itoa(d.Focus), nil
	case "tab":
		return strconv.Itoa(d.Tab()), nil
	case "active":
		return interp.Bool(strings.EqualFold(u.active, d.Name)), nil
	case "hwnd", "mapw", "maph":
		return "0", nil
	}
	return d.Name, nil
}

// modal runs $dialog(name,table): it shows the dialog and processes other
// work until it closes, then returns the result control's value when the
// ok button closed it. With no display it closes as cancelled unless a
// script closes it during init.
func (u *UI) modal(c *interp.Call) (string, error) {
	d, err := u.open(c.F, c.Arg(0), c.Arg(1), true)
	if err != nil {
		return "", err
	}
	if !d.closed && !u.Front.Interactive() {
		u.close(d, false)
	}
	for !d.closed {
		fn := <-u.rt.Posted()
		fn()
	}
	if d.okHit && d.Result != 0 {
		if ctl := d.Control(d.Result); ctl != nil {
			return ctl.Value(), nil
		}
	}
	return "", nil
}
