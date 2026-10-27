package ui

import (
	"image"
	"image/color"
	"image/draw"
	"strconv"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

func (u *UI) installPrompts() {
	rt := u.rt
	rt.RegisterIdent("input", u.identInput)
	rt.RegisterIdent("sfile", func(c *interp.Call) (string, error) {
		return u.prompt(Prompt{Kind: "file", Dir: c.Arg(0), Title: c.Arg(1), OkText: c.Arg(2)}, false)
	})
	rt.RegisterIdent("sdir", func(c *interp.Call) (string, error) {
		return u.prompt(Prompt{Kind: "dir", Dir: c.Arg(0), Title: c.Arg(1)}, false)
	})
	rt.RegisterIdent("msfile", u.identMsfile)
	rt.RegisterIdent("sfstate", func(*interp.Call) (string, error) { return u.sfstate, nil })
}

// prompt shows a modal prompt and waits for the answer, processing other
// work meanwhile. Headless, prompts are cancelled.
func (u *UI) prompt(p Prompt, all bool) (string, error) {
	var answer []string
	var ok, done bool
	u.Front.Prompt(p, func(a []string, accepted bool) { answer, ok, done = a, accepted, true })
	for !done {
		fn := <-u.rt.Posted()
		fn()
	}
	u.sfstate = ""
	if !ok {
		u.sfstate = "cancel"
		return "", nil
	}
	if all {
		u.msfiles = answer
		return strconv.Itoa(len(answer)), nil
	}
	if len(answer) == 0 {
		return "", nil
	}
	return answer[0], nil
}

// identInput implements $input(prompt, options, window, title, text).
// Options include e (editbox), p (password), o/y/n/v buttons and q/w/i/h
// icons; with y/n buttons the result is $true or $false.
func (u *UI) identInput(c *interp.Call) (string, error) {
	opts := c.Arg(1)
	v, err := u.prompt(Prompt{Kind: "input", Text: c.Arg(0), Options: opts, Title: c.Arg(3), Default: c.Arg(4)}, false)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(opts, "yn") && !strings.Contains(opts, "e") {
		return interp.Bool(v == "$true" || v == "yes"), nil
	}
	return v, nil
}

// identMsfile implements $msfile(dir,title,oktext) and $msfile(N).
func (u *UI) identMsfile(c *interp.Call) (string, error) {
	if n, ok := interp.ParseNumber(c.Arg(0)); ok && len(c.Args) == 1 {
		i := int(n)
		if i == 0 {
			return strconv.Itoa(len(u.msfiles)), nil
		}
		if i >= 1 && i <= len(u.msfiles) {
			return u.msfiles[i-1], nil
		}
		return "", nil
	}
	return u.prompt(Prompt{Kind: "files", Dir: c.Arg(0), Title: c.Arg(1), OkText: c.Arg(2)}, true)
}

// newCanvas returns a white picture-window bitmap.
func newCanvas(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	return img
}
