//go:build !nogui

package gui

import (
	"image"
	"image/color"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// maxStatusLines is how much output the status window keeps.
const maxStatusLines = 2000

// Status is the runtime's own status window: script output and errors,
// with a command line. It is not a script window, so scripts cannot see it.
type Status struct {
	mu     sync.Mutex
	w      *app.Window
	lines  []statusLine
	submit func(string)
	closed func()
}

type statusLine struct {
	at     time.Time
	window string
	text   string
	err    bool
}

// OpenStatus opens the status window. submit receives typed commands;
// closed runs when the user closes the window.
func OpenStatus(title string, submit func(string), closed func()) *Status {
	s := &Status{w: new(app.Window), submit: submit, closed: closed}
	s.w.Option(app.Title(title), app.Size(unit.Dp(720), unit.Dp(420)))
	go s.loop()
	return s
}

// Add appends a line; it is safe to call from any goroutine.
func (s *Status) Add(window, text string, isErr bool) {
	s.mu.Lock()
	s.lines = append(s.lines, statusLine{time.Now(), window, text, isErr})
	if len(s.lines) > maxStatusLines {
		s.lines = s.lines[len(s.lines)-maxStatusLines:]
	}
	s.mu.Unlock()
	s.w.Invalidate()
}

func (s *Status) loop() {
	th := material.NewTheme()
	var ops op.Ops
	var ed widget.Editor
	ed.SingleLine, ed.Submit = true, true
	list := widget.List{List: layout.List{Axis: layout.Vertical, ScrollToEnd: true}}
	focused := false
	for {
		switch e := s.w.Event().(type) {
		case app.DestroyEvent:
			if s.closed != nil {
				s.closed()
			}
			return
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			if !focused {
				gtx.Execute(key.FocusCmd{Tag: &ed})
				focused = true
			}
			for {
				ev, ok := ed.Update(gtx)
				if !ok {
					break
				}
				if sub, ok := ev.(widget.SubmitEvent); ok && sub.Text != "" {
					ed.SetText("")
					s.submit(sub.Text)
				}
			}
			s.mu.Lock()
			lines := append([]statusLine(nil), s.lines...)
			s.mu.Unlock()
			s.layout(gtx, th, &list, &ed, lines)
			e.Frame(gtx.Ops)
		}
	}
}

func (s *Status) layout(gtx layout.Context, th *material.Theme, list *widget.List, ed *widget.Editor, lines []statusLine) {
	paint.Fill(gtx.Ops, color.NRGBA{255, 255, 255, 255})
	editH := gtx.Dp(32)
	area := gtx
	area.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, gtx.Constraints.Max.Y-editH))
	layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(6)}.Layout(area, func(gtx layout.Context) layout.Dimensions {
		return material.List(th, list).Layout(gtx, len(lines), func(gtx layout.Context, i int) layout.Dimensions {
			l := lines[i]
			text := l.at.Format("15:04:05") + "  " + l.text
			if l.window != "" && l.window != "Status Window" && !l.err {
				text = l.at.Format("15:04:05") + "  [" + l.window + "] " + l.text
			}
			lbl := material.Label(th, unit.Sp(12), stripCodes(text))
			lbl.Color = color.NRGBA{40, 40, 40, 255}
			if l.err {
				lbl.Color = color.NRGBA{190, 30, 30, 255}
			}
			return layout.Inset{Bottom: unit.Dp(2)}.Layout(gtx, lbl.Layout)
		})
	})
	defer op.Offset(image.Pt(0, gtx.Constraints.Max.Y-editH)).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, editH))
	paint.FillShape(gtx.Ops, color.NRGBA{246, 246, 246, 255}, clip.Rect{Max: gtx.Constraints.Max}.Op())
	border(gtx, color.NRGBA{200, 200, 200, 255})
	e := material.Editor(th, ed, "Type a command, for example /server irc.example.net +6697")
	e.TextSize = unit.Sp(13)
	layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(7)}.Layout(gtx, e.Layout)
}
