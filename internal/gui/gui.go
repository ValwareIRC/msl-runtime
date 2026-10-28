//go:build !nogui

// Package gui shows script-created dialogs, custom windows and prompts as
// native desktop windows using Gio. It implements ui.Frontend: the runtime
// sends it snapshots of the model, and user input goes back to the runtime
// goroutine with Post. Build with -tags nogui to leave it out.
package gui

import (
	"image"
	"image/color"
	"image/draw"
	"strings"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/ui"
)

// Available reports whether this build includes the desktop GUI.
const Available = true

// GUI is the desktop frontend.
type GUI struct {
	rt *interp.Runtime
	u  *ui.UI

	mu   sync.Mutex
	wins map[string]*win // "d:name" for dialogs, "w:name" for windows
}

// New returns a GUI frontend for u. Call Main from the program's main
// goroutine afterwards.
func New(rt *interp.Runtime, u *ui.UI) *GUI {
	return &GUI{rt: rt, u: u, wins: map[string]*win{}}
}

// Main runs the platform event loop; it must be called from main.
func Main() { app.Main() }

// win is one native window and the latest snapshot it renders.
type win struct {
	g *GUI
	// th is per window: Gio's text shaper must not be shared between goroutines.
	th     *material.Theme
	key    string
	w      *app.Window
	mu     sync.Mutex
	dialog *ui.Dialog
	window *ui.Window
	pic    *image.RGBA
	prompt *promptState

	ctrls    map[int]*ctrlState
	radios   widget.Enum
	lines    widget.List
	lineBtns []widget.Clickable
	editbox  widget.Editor
	// editSeq is the last script change to the editbox that was applied.
	editSeq int
	// editFocused records that the editbox was given focus when the window opened.
	editFocused bool
	menuOpen    int // dialog menu shown, 0 for none
	menuBtns    map[int]*widget.Clickable
	popup       []ui.MenuItem
	popupBtn    []widget.Clickable
	popupAt     image.Point
	lastDown    time.Time
	inside      bool
	closing     bool

	// title and size are the options last applied to the native window;
	// framed records that it has drawn a frame, so Gio knows its real size.
	title  string
	size   image.Point
	framed bool
}

// ctrlState is the widget state of one dialog control.
type ctrlState struct {
	click widget.Clickable
	ed    widget.Editor
	b     widget.Bool
	f     widget.Float
	list  widget.List
	items []widget.Clickable
	text  string // last text pushed into ed from the model
}

func (g *GUI) Interactive() bool { return true }

// copyDialog takes a snapshot that the GUI goroutine can read safely.
func copyDialog(d *ui.Dialog) *ui.Dialog {
	c := *d
	c.Controls = make([]*ui.Control, len(d.Controls))
	for i, ctl := range d.Controls {
		cc := *ctl
		cc.Lines = append([]string(nil), ctl.Lines...)
		cc.Sel = append([]int(nil), ctl.Sel...)
		cc.Checked = append([]bool(nil), ctl.Checked...)
		c.Controls[i] = &cc
	}
	return &c
}

func copyWindow(w *ui.Window) (*ui.Window, *image.RGBA) {
	c := *w
	c.Lines = append([]ui.Line(nil), w.Lines...)
	c.Side = append([]ui.Line(nil), w.Side...)
	var pic *image.RGBA
	if w.Pic != nil {
		pic = image.NewRGBA(w.Pic.Rect)
		draw.Draw(pic, pic.Rect, w.Pic, image.Point{}, draw.Src)
	}
	c.Pic = nil
	return &c, pic
}

// dbu converts dialog coordinates to pixels for dialogs using dialog base units.
func dbu(d *ui.Dialog, x, y int) (int, int) {
	if d.Unit == "dbu" {
		return x * 7 / 4, y * 13 / 8
	}
	return x, y
}

func (g *GUI) DialogOpened(d *ui.Dialog) {
	snap := copyDialog(d)
	g.open("d:"+strings.ToLower(snap.Name), &win{dialog: snap, ctrls: map[int]*ctrlState{}, menuBtns: map[int]*widget.Clickable{}},
		dialogTitle(snap), dialogSize(snap))
}

func dialogTitle(d *ui.Dialog) string {
	if d.Title == "" {
		return d.Name
	}
	return d.Title
}

// dialogSize is a dialog's window size, including its menu bar.
func dialogSize(d *ui.Dialog) image.Point {
	w, h := dbu(d, d.W, d.H)
	if w <= 0 {
		w = 320
	}
	if h <= 0 {
		h = 220
	}
	return image.Pt(w, h+menuHeight(d))
}

func menuHeight(d *ui.Dialog) int {
	for _, c := range d.Controls {
		if c.Kind == "menu" && c.Menu == 0 {
			return 26
		}
	}
	return 0
}

func (g *GUI) DialogChanged(d *ui.Dialog) {
	if wn := g.find("d:" + strings.ToLower(d.Name)); wn != nil {
		wn.mu.Lock()
		wn.dialog = copyDialog(d)
		wn.mu.Unlock()
		wn.configure(dialogTitle(d), dialogSize(d))
		wn.w.Invalidate()
	}
}

func (g *GUI) DialogClosed(d *ui.Dialog) { g.close("d:" + strings.ToLower(d.Name)) }

func (g *GUI) WindowOpened(w *ui.Window) {
	if w.Hidden {
		return
	}
	snap, pic := copyWindow(w)
	wn := &win{window: snap, pic: pic}
	wn.lines.Axis = layout.Vertical
	wn.editbox.SingleLine, wn.editbox.Submit = true, true
	g.open("w:"+strings.ToLower(snap.Name), wn, windowTitle(snap), windowSize(snap, pic))
}

// windowSize is a custom window's size: a picture window fits its canvas
// (and editbox), other windows take their /window size or a default.
func windowSize(w *ui.Window, pic *image.RGBA) image.Point {
	if w.Picture && pic != nil {
		size := pic.Rect.Size()
		if w.Editbox {
			size.Y += editboxHeight
		}
		return size
	}
	size := image.Pt(w.W, w.H)
	if size.X <= 0 {
		size.X = 520
	}
	if size.Y <= 0 {
		size.Y = 340
	}
	return size
}

// windowTitle is a custom window's name and title, without @ when hidden.
func windowTitle(w *ui.Window) string {
	name := w.Name
	if w.HidePrefix {
		name = strings.TrimPrefix(name, "@")
	}
	if w.Title != "" && w.HidePrefix {
		return w.Title
	}
	return strings.TrimSpace(name + " " + w.Title)
}

func (g *GUI) WindowChanged(w *ui.Window) {
	wn := g.find("w:" + strings.ToLower(w.Name))
	if wn == nil {
		if !w.Hidden {
			g.WindowOpened(w)
		}
		return
	}
	snap, pic := copyWindow(w)
	wn.mu.Lock()
	wn.window, wn.pic = snap, pic
	wn.mu.Unlock()
	wn.configure(windowTitle(snap), windowSize(snap, pic))
	wn.w.Invalidate()
}

// configure applies a title and size to the native window, sending only what
// changed so a size the user chose is kept. Gio sizes a window from the last
// size it saw, which is zero until the first frame: an option sent alone
// before then collapses the window, so the size goes with it.
func (wn *win) configure(title string, size image.Point) {
	wn.mu.Lock()
	var opts []app.Option
	if title != wn.title {
		opts = append(opts, app.Title(title))
		wn.title = title
	}
	if size != wn.size || (!wn.framed && len(opts) > 0) {
		opts = append(opts, app.Size(unit.Dp(size.X), unit.Dp(size.Y)))
		wn.size = size
	}
	wn.mu.Unlock()
	if len(opts) > 0 {
		wn.w.Option(opts...)
	}
}

func (g *GUI) WindowClosed(w *ui.Window) { g.close("w:" + strings.ToLower(w.Name)) }

func (g *GUI) find(key string) *win {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.wins[key]
}

func (g *GUI) open(key string, wn *win, title string, size image.Point) {
	g.mu.Lock()
	if old := g.wins[key]; old != nil {
		old.closing = true
		old.w.Perform(system.ActionClose)
	}
	wn.g, wn.key, wn.w = g, key, new(app.Window)
	wn.title, wn.size = title, size
	g.wins[key] = wn
	g.mu.Unlock()
	wn.w.Option(app.Title(title), app.Size(unit.Dp(size.X), unit.Dp(size.Y)))
	go wn.loop()
}

func (g *GUI) close(key string) {
	g.mu.Lock()
	wn := g.wins[key]
	delete(g.wins, key)
	g.mu.Unlock()
	if wn != nil {
		wn.closing = true
		wn.w.Perform(system.ActionClose)
	}
}

// post runs fn on the runtime goroutine.
func (g *GUI) post(fn func()) { g.rt.Post(fn) }

// loop handles one window's events until it is destroyed.
func (wn *win) loop() {
	wn.th = material.NewTheme()
	var ops op.Ops
	for {
		switch e := wn.w.Event().(type) {
		case app.DestroyEvent:
			wn.g.mu.Lock()
			if wn.g.wins[wn.key] == wn {
				delete(wn.g.wins, wn.key)
			}
			wn.g.mu.Unlock()
			if !wn.closing {
				wn.userClosed()
			}
			return
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			wn.mu.Lock()
			wn.framed = true
			wn.frame(gtx)
			wn.mu.Unlock()
			e.Frame(gtx.Ops)
		}
	}
}

// userClosed reports a window closed by its user.
func (wn *win) userClosed() {
	g := wn.g
	switch {
	case wn.prompt != nil:
		p := wn.prompt
		g.post(func() { p.done(nil, false) })
	case wn.dialog != nil:
		name := wn.dialog.Name
		g.post(func() { g.u.CloseDialog(name) })
	case wn.window != nil:
		name := wn.window.Name
		g.post(func() { g.u.CloseWindow(name) })
	}
}

func (wn *win) frame(gtx layout.Context) {
	paint.Fill(gtx.Ops, color.NRGBA{240, 240, 240, 255})
	switch {
	case wn.prompt != nil:
		wn.layoutPrompt(gtx)
	case wn.dialog != nil:
		wn.layoutDialog(gtx)
	case wn.window != nil && wn.window.Picture:
		wn.layoutPicture(gtx)
	case wn.window != nil:
		wn.layoutText(gtx)
	}
	wn.layoutPopup(gtx)
}

// at lays out w at a fixed position and size.
func at(gtx layout.Context, x, y, w, h int, widget layout.Widget) {
	if w <= 0 || h <= 0 {
		return
	}
	defer op.Offset(image.Pt(gtx.Dp(unit.Dp(x)), gtx.Dp(unit.Dp(y)))).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(w)), gtx.Dp(unit.Dp(h))))
	widget(gtx)
}

func (wn *win) state(id int) *ctrlState {
	s := wn.ctrls[id]
	if s == nil {
		s = &ctrlState{}
		s.list.Axis = layout.Vertical
		wn.ctrls[id] = s
	}
	return s
}

// layoutDialog draws a dialog's menu bar and controls at their positions.
func (wn *win) layoutDialog(gtx layout.Context) {
	d := wn.dialog
	g := wn.g
	name := d.Name
	top := menuHeight(d)
	tab := d.Tab()
	// Tab pages belong to the first tab control; controls on other pages are hidden.
	tabGroup := map[int]bool{}
	for _, c := range d.Controls {
		if c.Kind == "tab" {
			tabGroup[c.ID] = true
		}
	}
	for _, c := range d.Controls {
		if !c.Visible || c.Kind == "menu" || c.Kind == "item" || c.Kind == "break" {
			continue
		}
		if c.Tab != 0 && tabGroup[c.Tab] && c.Kind != "tab" && c.Tab != tab {
			continue
		}
		x, y := dbu(d, c.X, c.Y)
		w, h := dbu(d, c.W, c.H)
		y += top
		s := wn.state(c.ID)
		id := c.ID
		gc := gtx
		if !c.Enabled {
			gc = gtx.Disabled()
		}
		switch c.Kind {
		case "text":
			at(gc, x, y, w, h+4, small(wn.th, c.Text).Layout)
		case "link":
			if s.click.Clicked(gc) {
				g.post(func() { g.u.Click(name, id) })
			}
			l := small(wn.th, c.Text)
			l.Color = color.NRGBA{0, 0, 200, 255}
			at(gc, x, y, w, h, func(gtx layout.Context) layout.Dimensions { return s.click.Layout(gtx, l.Layout) })
		case "button":
			if s.click.Clicked(gc) {
				g.post(func() { g.u.Click(name, id) })
			}
			at(gc, x, y, w, h, func(gtx layout.Context) layout.Dimensions { return classicButton(gtx, wn.th, &s.click, c.Text) })
		case "check":
			s.b.Value = c.State == 1
			if s.b.Update(gc) {
				g.post(func() { g.u.Click(name, id) })
			}
			cb := material.CheckBox(wn.th, &s.b, c.Text)
			cb.Size, cb.TextSize = unit.Dp(14), unit.Sp(12)
			at(gc, x, y-2, w, h+5, cb.Layout)
		case "radio":
			key := keyOf(id)
			if c.State == 1 {
				wn.radios.Value = key
			}
			if wn.radios.Update(gc) && wn.radios.Value == key {
				g.post(func() { g.u.Click(name, id) })
			}
			rb := material.RadioButton(wn.th, &wn.radios, key, c.Text)
			rb.Size, rb.TextSize = unit.Dp(14), unit.Sp(12)
			at(gc, x, y-2, w, h+5, rb.Layout)
		case "edit", "combo":
			wn.layoutEdit(gc, c, s, x, y, w, h)
		case "list":
			wn.layoutList(gc, c, s, x, y, w, h)
		case "box":
			at(gc, x, y, w, h, func(gtx layout.Context) layout.Dimensions {
				border(gtx, color.NRGBA{150, 150, 150, 255})
				return small(wn.th, c.Text).Layout(gtx)
			})
		case "scroll":
			span := float32(c.Max - c.Min)
			if span <= 0 {
				span = 1
			}
			s.f.Value = float32(c.Pos-c.Min) / span
			if s.f.Update(gc) {
				pos := c.Min + int(s.f.Value*span+0.5)
				g.post(func() { g.u.SetScroll(name, id, pos) })
			}
			at(gc, x, y, w, h, material.Slider(wn.th, &s.f).Layout)
		case "tab":
			if c.X >= 0 {
				wn.layoutTabs(gc, d, x, y, w)
			}
		case "icon":
			at(gc, x, y, w, h, func(gtx layout.Context) layout.Dimensions {
				border(gtx, color.NRGBA{180, 180, 180, 255})
				return layout.Dimensions{Size: gtx.Constraints.Max}
			})
		}
	}
	if top > 0 {
		wn.layoutMenuBar(gtx, d)
	}
}

func keyOf(id int) string { return "r" + itoa(id) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// small is the compact 12pt label used inside dialogs, like Windows' dialog font.
func small(th *material.Theme, text string) material.LabelStyle {
	l := material.Label(th, unit.Sp(12), text)
	l.MaxLines = 1
	return l
}

// classicButton draws a compact push button with a centred label.
func classicButton(gtx layout.Context, th *material.Theme, click *widget.Clickable, text string) layout.Dimensions {
	return click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		bg := color.NRGBA{225, 225, 225, 255}
		if click.Hovered() {
			bg = color.NRGBA{229, 241, 251, 255}
		}
		size := gtx.Constraints.Max
		paint.FillShape(gtx.Ops, bg, clip.Rect{Max: size}.Op())
		border(gtx, color.NRGBA{173, 173, 173, 255})
		layout.Center.Layout(gtx, small(th, strings.ReplaceAll(text, "&", "")).Layout)
		return layout.Dimensions{Size: size}
	})
}

// border draws a 1px frame around the current constraints.
func border(gtx layout.Context, col color.NRGBA) {
	r := image.Rectangle{Max: gtx.Constraints.Max}
	paint.FillShape(gtx.Ops, col, clip.Stroke{Path: clip.Rect(r).Path(), Width: 1}.Op())
}

func (wn *win) layoutEdit(gtx layout.Context, c *ui.Control, s *ctrlState, x, y, w, h int) {
	g, name, id := wn.g, wn.dialog.Name, c.ID
	s.ed.SingleLine = !c.Has("multi")
	s.ed.ReadOnly = c.Has("read")
	if c.Has("pass") {
		s.ed.Mask = '*'
	}
	model := c.Text
	if c.Has("multi") && len(c.Lines) > 0 {
		model = strings.Join(c.Lines, "\n")
	}
	if model != s.text {
		s.text = model
		if s.ed.Text() != model {
			s.ed.SetText(model)
		}
	}
	for {
		ev, ok := s.ed.Update(gtx)
		if !ok {
			break
		}
		if _, changed := ev.(widget.ChangeEvent); changed {
			text := s.ed.Text()
			s.text = text
			g.post(func() { g.u.SetText(name, id, text) })
		}
	}
	at(gtx, x, y, w, h, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, color.NRGBA{255, 255, 255, 255}, clip.Rect{Max: gtx.Constraints.Max}.Op())
		border(gtx, color.NRGBA{120, 120, 120, 255})
		ed := material.Editor(wn.th, &s.ed, "")
		ed.TextSize = unit.Sp(12)
		return layout.Inset{Left: unit.Dp(3), Right: unit.Dp(3), Top: unit.Dp(1)}.Layout(gtx, ed.Layout)
	})
	if c.Kind == "combo" && len(c.Lines) > 0 && !c.Has("drop") {
		wn.layoutList(gtx, c, s, x, y+h, w, min(len(c.Lines)*20+4, 120))
	}
}

func (wn *win) layoutList(gtx layout.Context, c *ui.Control, s *ctrlState, x, y, w, h int) {
	g, name, id := wn.g, wn.dialog.Name, c.ID
	for len(s.items) < len(c.Lines) {
		s.items = append(s.items, widget.Clickable{})
	}
	selected := map[int]bool{}
	for _, n := range c.Sel {
		selected[n] = true
	}
	for i := range c.Lines {
		if s.items[i].Clicked(gtx) {
			line := i + 1
			double := time.Since(wn.lastDown) < 400*time.Millisecond
			wn.lastDown = time.Now()
			g.post(func() { g.u.SelectLines(name, id, []int{line}, double) })
		}
	}
	at(gtx, x, y, w, h, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, color.NRGBA{255, 255, 255, 255}, clip.Rect{Max: gtx.Constraints.Max}.Op())
		border(gtx, color.NRGBA{120, 120, 120, 255})
		return material.List(wn.th, &s.list).Layout(gtx, len(c.Lines), func(gtx layout.Context, i int) layout.Dimensions {
			return s.items[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return lineRow(gtx, wn.th, c.Lines[i], selected[i+1], color.NRGBA{0, 0, 0, 255})
			})
		})
	})
}

// lineRow draws one list line, highlighted when selected.
func lineRow(gtx layout.Context, th *material.Theme, text string, sel bool, fg color.NRGBA) layout.Dimensions {
	l := small(th, text)
	l.Color = fg
	m := op.Record(gtx.Ops)
	dims := layout.Inset{Left: unit.Dp(4), Right: unit.Dp(4), Top: unit.Dp(1), Bottom: unit.Dp(1)}.Layout(gtx, l.Layout)
	call := m.Stop()
	if sel {
		paint.FillShape(gtx.Ops, color.NRGBA{190, 215, 245, 255}, clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, dims.Size.Y)}.Op())
	}
	call.Add(gtx.Ops)
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, dims.Size.Y)}
}

// layoutTabs draws the tab strip of a tab control.
func (wn *win) layoutTabs(gtx layout.Context, d *ui.Dialog, x, y, w int) {
	g, name := wn.g, d.Name
	var tabs []*ui.Control
	for _, c := range d.Controls {
		if c.Kind == "tab" {
			tabs = append(tabs, c)
		}
	}
	if len(tabs) == 0 {
		return
	}
	each := max(w/len(tabs), 30)
	for i, t := range tabs {
		s := wn.state(t.ID)
		id := t.ID
		if s.click.Clicked(gtx) {
			g.post(func() { g.u.Click(name, id) })
		}
		label := strings.ReplaceAll(t.Text, "&", "")
		at(gtx, x+i*each, y, each, 22, func(gtx layout.Context) layout.Dimensions {
			bg := color.NRGBA{215, 215, 215, 255}
			if t.State == 1 {
				bg = color.NRGBA{250, 250, 250, 255}
			}
			paint.FillShape(gtx.Ops, bg, clip.Rect{Max: gtx.Constraints.Max}.Op())
			border(gtx, color.NRGBA{150, 150, 150, 255})
			return s.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, small(wn.th, label).Layout)
			})
		})
	}
}

// layoutMenuBar draws a dialog's menu bar and the open menu's items.
func (wn *win) layoutMenuBar(gtx layout.Context, d *ui.Dialog) {
	g, name := wn.g, d.Name
	paint.FillShape(gtx.Ops, color.NRGBA{228, 228, 228, 255}, clip.Rect{Max: image.Pt(gtx.Constraints.Max.X, gtx.Dp(26))}.Op())
	x := 0
	for _, m := range d.Controls {
		if m.Kind != "menu" || m.Menu != 0 {
			continue
		}
		b := wn.menuBtns[m.ID]
		if b == nil {
			b = &widget.Clickable{}
			wn.menuBtns[m.ID] = b
		}
		if b.Clicked(gtx) {
			if wn.menuOpen == m.ID {
				wn.menuOpen = 0
			} else {
				wn.menuOpen = m.ID
			}
		}
		label := strings.ReplaceAll(m.Text, "&", "")
		width := len(label)*8 + 16
		mx := x
		at(gtx, mx, 0, width, 26, func(gtx layout.Context) layout.Dimensions {
			return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, small(wn.th, label).Layout)
			})
		})
		if wn.menuOpen == m.ID {
			var items []ui.MenuItem
			var ids []int
			for _, it := range d.Controls {
				if (it.Kind == "item" || it.Kind == "menu" || it.Kind == "break") && it.Menu == m.ID {
					items = append(items, ui.MenuItem{Label: strings.ReplaceAll(it.Text, "&", "")})
					ids = append(ids, it.ID)
				}
			}
			wn.showMenu(gtx, mx, 26, items, func(i int) {
				wn.menuOpen = 0
				id := ids[i]
				g.post(func() { g.u.Click(name, id) })
			})
		}
		x += width
	}
}

// showMenu draws a vertical menu at x, y and reports picks through pick.
func (wn *win) showMenu(gtx layout.Context, x, y int, items []ui.MenuItem, pick func(int)) {
	for len(wn.popupBtn) < len(items) {
		wn.popupBtn = append(wn.popupBtn, widget.Clickable{})
	}
	width := 140
	for _, it := range items {
		width = max(width, len(it.Label)*8+24)
	}
	for i, it := range items {
		if it.Label == "-" || strings.EqualFold(it.Label, "break") {
			continue
		}
		if wn.popupBtn[i].Clicked(gtx) {
			pick(i)
			return
		}
	}
	at(gtx, x, y, width, len(items)*24+4, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, color.NRGBA{252, 252, 252, 255}, clip.Rect{Max: gtx.Constraints.Max}.Op())
		border(gtx, color.NRGBA{140, 140, 140, 255})
		for i, it := range items {
			label := strings.Repeat("  ", it.Depth) + strings.ReplaceAll(it.Label, "&", "")
			btn := &wn.popupBtn[i]
			at(gtx, 2, 2+i*24, width-4, 24, func(gtx layout.Context) layout.Dimensions {
				if it.Label == "-" || strings.EqualFold(it.Label, "break") {
					paint.FillShape(gtx.Ops, color.NRGBA{200, 200, 200, 255}, clip.Rect{Min: image.Pt(4, 11), Max: image.Pt(gtx.Constraints.Max.X-4, 12)}.Op())
					return layout.Dimensions{Size: gtx.Constraints.Max}
				}
				return btn.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, small(wn.th, label).Layout)
					})
				})
			})
		}
		return layout.Dimensions{Size: gtx.Constraints.Max}
	})
}

// layoutPopup draws an open right-click menu for a custom window.
func (wn *win) layoutPopup(gtx layout.Context) {
	if wn.popup == nil || wn.window == nil {
		return
	}
	items, name := wn.popup, wn.window.Name
	wn.showMenu(gtx, wn.popupAt.X, wn.popupAt.Y, items, func(i int) {
		wn.popup = nil
		item := items[i]
		wn.g.post(func() { wn.g.u.RunMenuItem(name, item) })
	})
}

// openPopup evaluates a window's menu on the runtime goroutine and shows it.
func (wn *win) openPopup(at image.Point) {
	name := wn.window.Name
	g := wn.g
	g.post(func() {
		items := g.u.Menu(name)
		wn.mu.Lock()
		wn.popup, wn.popupAt = items, at
		if len(items) == 0 {
			wn.popup = nil
		}
		wn.mu.Unlock()
		wn.w.Invalidate()
	})
}

// layoutText draws a text or listbox custom window with its editbox.
func (wn *win) layoutText(gtx layout.Context) {
	w := wn.window
	g, name := wn.g, w.Name
	for len(wn.lineBtns) < len(w.Lines) {
		wn.lineBtns = append(wn.lineBtns, widget.Clickable{})
	}
	if w.Listbox {
		for i := range w.Lines {
			if wn.lineBtns[i].Clicked(gtx) {
				n := i + 1
				g.post(func() { g.u.SelectLine(name, n, false) })
			}
		}
	}
	editH := wn.updateEditbox(gtx)
	pg := gtx
	pg.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, gtx.Constraints.Max.Y-editH))
	wn.watchPointer(pg, false)
	area := gtx
	area.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, gtx.Constraints.Max.Y-editH))
	paint.FillShape(gtx.Ops, color.NRGBA{255, 255, 255, 255}, clip.Rect{Max: area.Constraints.Max}.Op())
	material.List(wn.th, &wn.lines).Layout(area, len(w.Lines), func(gtx layout.Context, i int) layout.Dimensions {
		l := w.Lines[i]
		fg := color.NRGBA{0, 0, 0, 255}
		if c, ok := ui.PaletteColor(l.Color); ok {
			fg = c
		}
		text := stripCodes(l.Text)
		if w.Listbox {
			return wn.lineBtns[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return lineRow(gtx, wn.th, text, l.Selected, fg)
			})
		}
		return lineRow(gtx, wn.th, text, false, fg)
	})
	wn.drawEditbox(gtx, editH)
}

// editboxHeight is the height of a custom window's editbox, in dp.
const editboxHeight = 30

// updateEditbox handles a custom window's editbox: typed changes go to the
// model (for $editbox and on KEYDOWN), Enter runs on INPUT. It returns the
// editbox height, 0 when the window has none.
func (wn *win) updateEditbox(gtx layout.Context) int {
	w := wn.window
	if !w.Editbox {
		return 0
	}
	g, name := wn.g, w.Name
	if !wn.editFocused {
		// Start with the editbox focused so typing works straight away.
		gtx.Execute(key.FocusCmd{Tag: &wn.editbox})
		wn.editFocused = true
	}
	if w.EditSeq != wn.editSeq {
		// A script set the text with /editbox; otherwise the user's typing wins.
		wn.editSeq = w.EditSeq
		if wn.editbox.Text() != w.EditText {
			wn.editbox.SetText(w.EditText)
			wn.editbox.SetCaret(len(w.EditText), len(w.EditText))
		}
	}
	for {
		ev, ok := wn.editbox.Update(gtx)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case widget.SubmitEvent:
			text := e.Text
			wn.editbox.SetText("")
			g.post(func() {
				g.u.EditboxChanged(name, "")
				g.u.Input(name, text)
			})
		case widget.ChangeEvent:
			text := wn.editbox.Text()
			g.post(func() { g.u.EditboxChanged(name, text) })
		}
	}
	return gtx.Dp(editboxHeight)
}

// drawEditbox draws the editbox along the bottom of a custom window.
func (wn *win) drawEditbox(gtx layout.Context, editH int) {
	if editH == 0 {
		return
	}
	defer op.Offset(image.Pt(0, gtx.Constraints.Max.Y-editH)).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, editH))
	paint.FillShape(gtx.Ops, color.NRGBA{255, 255, 255, 255}, clip.Rect{Max: gtx.Constraints.Max}.Op())
	border(gtx, color.NRGBA{150, 150, 150, 255})
	ed := material.Editor(wn.th, &wn.editbox, "")
	ed.TextSize = unit.Sp(13)
	layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(6)}.Layout(gtx, ed.Layout)
}

// layoutPicture draws a picture window's bitmap and reports mouse and keys.
func (wn *win) layoutPicture(gtx layout.Context) {
	editH := wn.updateEditbox(gtx)
	pg := gtx
	pg.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, gtx.Constraints.Max.Y-editH))
	if wn.pic != nil {
		st := clip.Rect{Max: pg.Constraints.Max}.Push(gtx.Ops)
		paint.NewImageOp(wn.pic).Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		st.Pop()
	}
	wn.watchPointer(pg, true)
	wn.drawEditbox(gtx, editH)
}

// watchPointer turns pointer and key events into window events.
func (wn *win) watchPointer(gtx layout.Context, picture bool) {
	g, name := wn.g, wn.window.Name
	tag := &wn.inside
	for {
		ev, ok := gtx.Event(
			pointer.Filter{Target: tag, Kinds: pointer.Press | pointer.Release | pointer.Move | pointer.Drag | pointer.Enter | pointer.Leave | pointer.Scroll,
				ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}},
			key.Filter{Focus: tag},
		)
		if !ok {
			break
		}
		switch e := ev.(type) {
		case pointer.Event:
			m := ui.Mouse{X: int(e.Position.X), Y: int(e.Position.Y)}
			if e.Buttons.Contain(pointer.ButtonPrimary) {
				m.Key |= 1
			}
			if e.Modifiers.Contain(key.ModCtrl) {
				m.Key |= 2
			}
			if e.Modifiers.Contain(key.ModShift) {
				m.Key |= 4
			}
			if e.Modifiers.Contain(key.ModAlt) {
				m.Key |= 8
			}
			kinds := mouseKinds(wn, e, picture)
			for _, k := range kinds {
				k := k
				g.post(func() { g.u.MouseEvent(name, k, m) })
			}
			if e.Kind == pointer.Scroll {
				m.DY = int(e.Scroll.Y)
			}
			if e.Kind == pointer.Press {
				if wn.window.Editbox {
					gtx.Execute(key.FocusCmd{Tag: &wn.editbox})
				} else {
					gtx.Execute(key.FocusCmd{Tag: tag})
				}
				g.post(func() { g.u.Activate(name) })
				if e.Buttons.Contain(pointer.ButtonSecondary) {
					wn.openPopup(image.Pt(m.X, m.Y))
				} else {
					wn.popup = nil
				}
			}
		case key.Event:
			kind := "KEYDOWN"
			if e.State == key.Release {
				kind = "KEYUP"
			}
			code, char := keyCode(e.Name)
			g.post(func() { g.u.Key(name, kind, code, char, false) })
		}
	}
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, tag)
	area.Pop()
}

// mouseKinds maps a pointer event to the menu events it triggers.
func mouseKinds(wn *win, e pointer.Event, picture bool) []string {
	switch e.Kind {
	case pointer.Move, pointer.Drag:
		if picture {
			return []string{"mouse"}
		}
	case pointer.Leave:
		return []string{"leave"}
	case pointer.Scroll:
		return []string{"wheel"}
	case pointer.Press:
		if e.Buttons.Contain(pointer.ButtonSecondary) {
			return []string{"rclick"}
		}
		double := time.Since(wn.lastDown) < 400*time.Millisecond
		wn.lastDown = time.Now()
		if double {
			return []string{"sclick", "dclick"}
		}
		return []string{"sclick"}
	case pointer.Release:
		return []string{"uclick"}
	}
	return nil
}

// keyCode converts a Gio key name to a Windows virtual-key code and character.
func keyCode(n key.Name) (int, rune) {
	s := string(n)
	if len(s) == 1 {
		r := rune(s[0])
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		return int(r), rune(s[0])
	}
	codes := map[key.Name]int{
		key.NameLeftArrow: 37, key.NameUpArrow: 38, key.NameRightArrow: 39, key.NameDownArrow: 40,
		key.NameReturn: 13, key.NameEnter: 13, key.NameEscape: 27, key.NameSpace: 32, key.NameTab: 9,
		key.NameDeleteBackward: 8, key.NameDeleteForward: 46, key.NameHome: 36, key.NameEnd: 35,
		key.NamePageUp: 33, key.NamePageDown: 34, key.NameF1: 112, key.NameF2: 113, key.NameF3: 114,
		key.NameF4: 115, key.NameF5: 116, key.NameF6: 117, key.NameF7: 118, key.NameF8: 119,
		key.NameF9: 120, key.NameF10: 121, key.NameF11: 122, key.NameF12: 123,
	}
	return codes[n], 0
}

// stripCodes removes IRC formatting codes for display.
func stripCodes(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case 2, 15, 22, 29, 30, 31:
		case 3:
			j := i + 1
			for k := 0; k < 2 && j < len(s) && s[j] >= '0' && s[j] <= '9'; k++ {
				j++
			}
			if j+1 < len(s) && s[j] == ',' && s[j+1] >= '0' && s[j+1] <= '9' {
				j++
				for k := 0; k < 2 && j < len(s) && s[j] >= '0' && s[j] <= '9'; k++ {
					j++
				}
			}
			i = j - 1
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
