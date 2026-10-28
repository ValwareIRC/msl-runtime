//go:build !nogui

package gui

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/ValwareIRC/msl-runtime/internal/ui"
)

// promptState is an open $input, $sfile, $msfile or $sdir prompt.
type promptState struct {
	p      ui.Prompt
	done   func([]string, bool)
	ed     widget.Editor
	ok     widget.Clickable
	cancel widget.Clickable
	yes    widget.Clickable
	no     widget.Clickable
	list   widget.List
	items  []widget.Clickable
	files  []string
	chosen map[int]bool
}

// Prompt opens a prompt window; done runs on the runtime goroutine.
func (g *GUI) Prompt(p ui.Prompt, done func([]string, bool)) {
	ps := &promptState{p: p, done: done, chosen: map[int]bool{}}
	ps.ed.SingleLine, ps.ed.Submit = true, true
	ps.list.Axis = layout.Vertical
	if strings.Contains(p.Options, "p") {
		ps.ed.Mask = '*'
	}
	switch p.Kind {
	case "input":
		ps.ed.SetText(p.Default)
	default:
		dir := p.Dir
		if dir == "" {
			dir, _ = os.Getwd()
		}
		ps.ed.SetText(dir)
		ps.files = listDir(dir, p.Kind == "dir")
	}
	title := p.Title
	if title == "" {
		title = map[string]string{"input": "Input", "file": "Select file", "files": "Select files", "dir": "Select folder"}[p.Kind]
	}
	height := 150
	if p.Kind != "input" {
		height = 380
	}
	g.open("p:"+title, &win{prompt: ps}, title, image.Pt(420, height))
}

// listDir returns the entries a file prompt offers: folders first, then files.
func listDir(dir string, dirsOnly bool) []string {
	pattern := "*"
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		dir, pattern = filepath.Dir(dir), filepath.Base(dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var dirs, files []string
	for _, e := range entries {
		switch {
		case e.IsDir():
			dirs = append(dirs, e.Name()+string(filepath.Separator))
		case !dirsOnly:
			if ok, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(e.Name())); ok {
				files = append(files, e.Name())
			}
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)
	return append(append([]string{".." + string(filepath.Separator)}, dirs...), files...)
}

func (wn *win) finishPrompt(answer []string, ok bool) {
	ps := wn.prompt
	wn.closing = true
	wn.g.close(wn.key)
	wn.g.post(func() { ps.done(answer, ok) })
}

func (wn *win) layoutPrompt(gtx layout.Context) {
	ps := wn.prompt
	th := wn.th
	yesNo := strings.ContainsAny(ps.p.Options, "yn") && !strings.Contains(ps.p.Options, "e")
	for {
		ev, ok := ps.ed.Update(gtx)
		if !ok {
			break
		}
		if _, submit := ev.(widget.SubmitEvent); submit {
			wn.acceptPrompt()
			return
		}
	}
	switch {
	case ps.ok.Clicked(gtx), ps.yes.Clicked(gtx):
		if yesNo {
			wn.finishPrompt([]string{"$true"}, true)
			return
		}
		wn.acceptPrompt()
		return
	case ps.no.Clicked(gtx):
		wn.finishPrompt([]string{"$false"}, true)
		return
	case ps.cancel.Clicked(gtx):
		wn.finishPrompt(nil, false)
		return
	}
	for i := range ps.files {
		if i < len(ps.items) && ps.items[i].Clicked(gtx) {
			wn.pickFile(i)
		}
	}
	layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := []layout.FlexChild{}
		if ps.p.Text != "" {
			children = append(children, layout.Rigid(material.Body1(th, ps.p.Text).Layout),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout))
		}
		if !yesNo {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return field(gtx, material.Editor(th, &ps.ed, ""))
			}))
		}
		if ps.p.Kind != "input" {
			for len(ps.items) < len(ps.files) {
				ps.items = append(ps.items, widget.Clickable{})
			}
			children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return material.List(th, &ps.list).Layout(gtx, len(ps.files), func(gtx layout.Context, i int) layout.Dimensions {
						return ps.items[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return lineRow(gtx, th, ps.files[i], ps.chosen[i], color.NRGBA{0, 0, 0, 255})
						})
					})
				})
			}))
		} else {
			children = append(children, layout.Flexed(1, layout.Spacer{}.Layout))
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			buttons := []layout.FlexChild{layout.Flexed(1, layout.Spacer{}.Layout)}
			add := func(c *widget.Clickable, label string) {
				buttons = append(buttons, layout.Rigid(material.Button(th, c, label).Layout),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout))
			}
			if yesNo {
				add(&ps.yes, "Yes")
				add(&ps.no, "No")
			} else {
				ok := ps.p.OkText
				if ok == "" {
					ok = "OK"
				}
				add(&ps.ok, ok)
			}
			add(&ps.cancel, "Cancel")
			return layout.Flex{}.Layout(gtx, buttons...)
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

func field(gtx layout.Context, ed material.EditorStyle) layout.Dimensions {
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, color.NRGBA{255, 255, 255, 255}, clip.Rect{Max: gtx.Constraints.Min}.Op())
			border(gtx, color.NRGBA{120, 120, 120, 255})
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(5)).Layout(gtx, ed.Layout)
		}),
	)
}

// pickFile handles a click in a file prompt's list: folders are opened,
// files are selected (several for $msfile).
func (wn *win) pickFile(i int) {
	ps := wn.prompt
	cur := ps.ed.Text()
	if st, err := os.Stat(cur); err != nil || !st.IsDir() {
		cur = filepath.Dir(cur)
	}
	name := ps.files[i]
	if strings.HasSuffix(name, string(filepath.Separator)) {
		next := filepath.Clean(filepath.Join(cur, name))
		ps.ed.SetText(next)
		ps.files, ps.chosen, ps.items = listDir(next, ps.p.Kind == "dir"), map[int]bool{}, nil
		return
	}
	if ps.p.Kind == "files" {
		ps.chosen[i] = !ps.chosen[i]
		return
	}
	ps.chosen = map[int]bool{i: true}
	ps.ed.SetText(filepath.Join(cur, name))
}

func (wn *win) acceptPrompt() {
	ps := wn.prompt
	text := ps.ed.Text()
	if ps.p.Kind == "files" {
		var out []string
		dir := text
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			dir = filepath.Dir(dir)
		}
		for i, on := range ps.chosen {
			if on {
				out = append(out, filepath.Join(dir, ps.files[i]))
			}
		}
		sort.Strings(out)
		wn.finishPrompt(out, len(out) > 0)
		return
	}
	wn.finishPrompt([]string{text}, true)
}
