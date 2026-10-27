package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"strconv"
	"strings"

	"golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/math/fixed"

	"github.com/ValwareIRC/msl-runtime/internal/data"
	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/storage"
)

// mircPalette holds mIRC's 16 standard colours followed by the 83 extended
// IRC colours (indexes 16-98), as 0xRRGGBB.
var mircPalette = []uint32{
	0xFFFFFF, 0x000000, 0x00007F, 0x009300, 0xFF0000, 0x7F0000, 0x9C009C, 0xFC7F00,
	0xFFFF00, 0x00FC00, 0x009393, 0x00FFFF, 0x0000FC, 0xFF00FF, 0x7F7F7F, 0xD2D2D2,
	0x470000, 0x472100, 0x474700, 0x324700, 0x004700, 0x00472C, 0x004747, 0x002747, 0x000047, 0x2E0047, 0x470047, 0x47002A,
	0x740000, 0x743A00, 0x747400, 0x517400, 0x007400, 0x007449, 0x007474, 0x004074, 0x000074, 0x4B0074, 0x740074, 0x740045,
	0xB50000, 0xB56300, 0xB5B500, 0x7DB500, 0x00B500, 0x00B571, 0x00B5B5, 0x0063B5, 0x0000B5, 0x7500B5, 0xB500B5, 0xB5006B,
	0xFF0000, 0xFF8C00, 0xFFFF00, 0xB2FF00, 0x00FF00, 0x00FFA0, 0x00FFFF, 0x008CFF, 0x0000FF, 0xA500FF, 0xFF00FF, 0xFF0098,
	0xFF5959, 0xFFB459, 0xFFFF71, 0xCFFF60, 0x6FFF6F, 0x65FFC9, 0x6DFFFF, 0x59B4FF, 0x5959FF, 0xC459FF, 0xFF66FF, 0xFF59BC,
	0xFF9C9C, 0xFFD39C, 0xFFFF9C, 0xE2FF9C, 0x9CFF9C, 0x9CFFDB, 0x9CFFFF, 0x9CD3FF, 0x9C9CFF, 0xDC9CFF, 0xFF9CFF, 0xFF94D3,
	0x000000, 0x131313, 0x282828, 0x363636, 0x4D4D4D, 0x656565, 0x818181, 0x9F9F9F, 0xBCBCBC, 0xE2E2E2, 0xFFFFFF,
}

// PaletteColor returns mIRC colour index i (0-98), or ok false when out of range.
func PaletteColor(i int) (color.NRGBA, bool) {
	if i < 0 || i >= len(mircPalette) {
		return color.NRGBA{}, false
	}
	c := mircPalette[i]
	return color.NRGBA{uint8(c >> 16), uint8(c >> 8), uint8(c), 255}, true
}

// toColor converts a /draw colour: a palette index, or with rgb set an
// mIRC $rgb value (red in the low byte).
func toColor(v string, rgb bool) color.RGBA {
	n := interp.Int(v)
	if !rgb {
		if n < 0 || n >= len(mircPalette) {
			n = 1
		}
		c := mircPalette[n]
		return color.RGBA{uint8(c >> 16), uint8(c >> 8), uint8(c), 255}
	}
	return color.RGBA{uint8(n), uint8(n >> 8), uint8(n >> 16), 255}
}

// rgbValue encodes a colour as an mIRC $rgb value.
func rgbValue(c color.RGBA) int { return int(c.R) | int(c.G)<<8 | int(c.B)<<16 }

// canvas wraps a picture window's bitmap with mIRC's drawing modes.
type canvas struct {
	img     *image.RGBA
	inverse bool
}

func (c canvas) set(x, y int, col color.RGBA) {
	if !(image.Point{x, y}.In(c.img.Rect)) {
		return
	}
	if c.inverse {
		o := c.img.RGBAAt(x, y)
		c.img.SetRGBA(x, y, color.RGBA{^o.R, ^o.G, ^o.B, 255})
		return
	}
	c.img.SetRGBA(x, y, col)
}

// dot draws a filled circle of the given diameter.
func (c canvas) dot(cx, cy, size int, col color.RGBA) {
	if size <= 1 {
		c.set(cx, cy, col)
		return
	}
	r := float64(size) / 2
	for y := -size; y <= size; y++ {
		for x := -size; x <= size; x++ {
			if float64(x*x+y*y) <= r*r {
				c.set(cx+x, cy+y, col)
			}
		}
	}
}

// line draws a line of the given thickness.
func (c canvas) line(x0, y0, x1, y1, size int, col color.RGBA) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := sign(x1-x0), sign(y1-y0)
	e := dx + dy
	for {
		c.dot(x0, y0, size, col)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * e
		if e2 >= dy {
			e += dy
			x0 += sx
		}
		if e2 <= dx {
			e += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0
}

func (c canvas) fillRect(r image.Rectangle, col color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c.set(x, y, col)
		}
	}
}

// inEllipse reports whether point p lies in the ellipse inscribed in r.
func inEllipse(px, py float64, r image.Rectangle) bool {
	rx, ry := float64(r.Dx())/2, float64(r.Dy())/2
	if rx <= 0 || ry <= 0 {
		return false
	}
	cx, cy := float64(r.Min.X)+rx, float64(r.Min.Y)+ry
	dx, dy := (px-cx)/rx, (py-cy)/ry
	return dx*dx+dy*dy <= 1
}

// inRoundRect reports whether p lies in r with corner ellipses of cw x ch.
func inRoundRect(px, py float64, r image.Rectangle, cw, ch int) bool {
	if px < float64(r.Min.X) || py < float64(r.Min.Y) || px >= float64(r.Max.X) || py >= float64(r.Max.Y) {
		return false
	}
	hw, hh := float64(cw)/2, float64(ch)/2
	cx := math.Max(float64(r.Min.X)+hw, math.Min(px, float64(r.Max.X)-hw))
	cy := math.Max(float64(r.Min.Y)+hh, math.Min(py, float64(r.Max.Y)-hh))
	if hw <= 0 || hh <= 0 {
		return true
	}
	dx, dy := (px-cx)/hw, (py-cy)/hh
	return dx*dx+dy*dy <= 1
}

// shape draws a rectangle, ellipse or rounded rectangle outline or fill.
func (c canvas) shape(r image.Rectangle, size int, col color.RGBA, fill, ellipse, round bool, cw, ch int) {
	inside := func(x, y int) bool {
		px, py := float64(x)+0.5, float64(y)+0.5
		switch {
		case ellipse:
			return inEllipse(px, py, r)
		case round:
			return inRoundRect(px, py, r, cw, ch)
		}
		return image.Pt(x, y).In(r)
	}
	inner := r.Inset(max(size, 1))
	innerInside := func(x, y int) bool {
		px, py := float64(x)+0.5, float64(y)+0.5
		switch {
		case ellipse:
			return inEllipse(px, py, inner)
		case round:
			return inRoundRect(px, py, inner, max(cw-2*size, 0), max(ch-2*size, 0))
		}
		return image.Pt(x, y).In(inner)
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if inside(x, y) && (fill || !innerInside(x, y)) {
				c.set(x, y, col)
			}
		}
	}
}

// flood fills from (x, y): with surface set, every connected pixel of the
// target colour; otherwise everything up to the border colour.
func (c canvas) flood(x, y int, col, other color.RGBA, surface bool) {
	if !(image.Point{x, y}.In(c.img.Rect)) {
		return
	}
	match := func(p color.RGBA) bool {
		if surface {
			return p == other
		}
		return p != other && p != col
	}
	if !match(c.img.RGBAAt(x, y)) {
		return
	}
	stack := []image.Point{{x, y}}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !p.In(c.img.Rect) || !match(c.img.RGBAAt(p.X, p.Y)) {
			continue
		}
		c.img.SetRGBA(p.X, p.Y, col)
		stack = append(stack, image.Pt(p.X+1, p.Y), image.Pt(p.X-1, p.Y), image.Pt(p.X, p.Y+1), image.Pt(p.X, p.Y-1))
	}
}

// text draws text with its top-left corner at (x, y), optionally clipped.
func (c canvas) text(face font.Face, x, y int, s string, fg color.RGBA, bg *color.RGBA, clip image.Rectangle) {
	if bg != nil {
		w := TextWidth(face, s)
		c.fillRect(image.Rect(x, y, x+w, y+TextHeight(face)).Intersect(clip), *bg)
	}
	dst := draw.Image(c.img)
	if !clip.Empty() {
		dst = clipImage{c.img, clip}
	}
	d := font.Drawer{Dst: dst, Src: image.NewUniform(fg), Face: face,
		Dot: fixed.P(x, y+face.Metrics().Ascent.Ceil())}
	d.DrawString(s)
}

// clipImage restricts drawing to a rectangle.
type clipImage struct {
	*image.RGBA
	r image.Rectangle
}

func (c clipImage) Bounds() image.Rectangle { return c.RGBA.Bounds().Intersect(c.r) }
func (c clipImage) Set(x, y int, col color.Color) {
	if image.Pt(x, y).In(c.r) {
		c.RGBA.Set(x, y, col)
	}
}

func (u *UI) installDraw() {
	rt := u.rt
	draw := func(name string, fn func(*interp.CmdCall, *Window, string, []string) error) {
		rt.RegisterCommand(name, interp.Command{Fn: func(c *interp.CmdCall) error {
			sw, rest := c.Switches()
			f := strings.Fields(rest)
			if len(f) == 0 {
				return c.InvalidParams()
			}
			w := u.window(f[0])
			if w == nil || w.Pic == nil {
				return c.Errorf("'%s' not a picture window", f[0])
			}
			if len(f) > 1 {
				if err := fn(c, w, sw, f[1:]); err != nil {
					return err
				}
			}
			if !strings.Contains(sw, "n") {
				u.Front.WindowChanged(w)
			}
			return nil
		}})
	}
	draw("drawdot", u.drawDot)
	draw("drawline", u.drawLine)
	draw("drawrect", u.drawRect)
	draw("drawfill", u.drawFill)
	draw("drawtext", u.drawText)
	draw("drawsize", func(c *interp.CmdCall, w *Window, sw string, f []string) error {
		if len(f) < 2 {
			return c.InvalidParams()
		}
		resizeCanvas(w, min(interp.Int(f[0]), 8192), min(interp.Int(f[1]), 8192))
		return nil
	})
	draw("drawscroll", u.drawScroll)
	draw("drawcopy", u.drawCopy)
	draw("drawpic", u.drawPic)
	draw("drawsave", u.drawSave)
	draw("drawrot", u.drawRot)
	draw("drawreplace", func(c *interp.CmdCall, w *Window, sw string, f []string) error {
		if len(f) < 2 {
			return c.InvalidParams()
		}
		rgb := strings.Contains(sw, "r")
		from, to := toColor(f[0], rgb), toColor(f[1], rgb)
		r := w.Pic.Rect
		if len(f) >= 6 {
			r = rect(f[2:6])
		}
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				if image.Pt(x, y).In(w.Pic.Rect) && w.Pic.RGBAAt(x, y) == from {
					w.Pic.SetRGBA(x, y, to)
				}
			}
		}
		return nil
	})
	rt.RegisterIdent("getdot", func(c *interp.Call) (string, error) {
		w := u.window(c.Arg(0))
		if w == nil || w.Pic == nil {
			return "", nil
		}
		x, y := interp.Int(c.Arg(1)), interp.Int(c.Arg(2))
		if !(image.Point{x, y}.In(w.Pic.Rect)) {
			return "", nil
		}
		return strconv.Itoa(rgbValue(w.Pic.RGBAAt(x, y))), nil
	})
	rt.RegisterIdent("rgb", identRGB)
	rt.RegisterIdent("click", u.identClick)
	rt.RegisterIdent("pic", u.identPic)
	rt.RegisterIdent("inrect", func(c *interp.Call) (string, error) {
		return interp.Bool(image.Pt(argInt(c, 0), argInt(c, 1)).In(rectArgs(c, 2))), nil
	})
	rt.RegisterIdent("inellipse", func(c *interp.Call) (string, error) {
		return interp.Bool(inEllipse(float64(argInt(c, 0))+0.5, float64(argInt(c, 1))+0.5, rectArgs(c, 2))), nil
	})
	rt.RegisterIdent("inroundrect", func(c *interp.Call) (string, error) {
		return interp.Bool(inRoundRect(float64(argInt(c, 0))+0.5, float64(argInt(c, 1))+0.5, rectArgs(c, 2), argInt(c, 6), argInt(c, 7))), nil
	})
	rt.RegisterIdent("inpoly", func(c *interp.Call) (string, error) {
		return interp.Bool(inPolygon(float64(argInt(c, 0)), float64(argInt(c, 1)), points(c.Args[2:]))), nil
	})
	rt.RegisterIdent("onpoly", func(c *interp.Call) (string, error) {
		n1, n2 := argInt(c, 0), argInt(c, 1)
		pts := points(c.Args[2:])
		if n1 < 1 || n2 < 1 || n1+n2 > len(pts) {
			return "", c.InvalidParams()
		}
		return interp.Bool(polygonsOverlap(pts[:n1], pts[n1:n1+n2])), nil
	})
	rt.RegisterIdent("width", func(c *interp.Call) (string, error) {
		text, face := fontArgs(c)
		return strconv.Itoa(TextWidth(face, text)), nil
	})
	rt.RegisterIdent("height", func(c *interp.Call) (string, error) {
		_, face := fontArgs(c)
		return strconv.Itoa(TextHeight(face)), nil
	})
	rt.RegisterIdent("wrap", func(c *interp.Call) (string, error) {
		opts := c.Arg(4)
		face := Face(c.Arg(1), interp.Int(c.Arg(2)), strings.Contains(opts, "b") && !strings.Contains(opts, "b0"))
		text := c.Arg(0)
		if strings.Contains(opts, "p") && !strings.Contains(opts, "p0") {
			text = stripControl(text)
		}
		lines := wrapText(face, text, max(interp.Int(c.Arg(3)), 1), !strings.Contains(opts, "w0"))
		n := interp.Int(c.Arg(5))
		if n == 0 {
			return strconv.Itoa(len(lines)), nil
		}
		if n < 1 || n > len(lines) {
			return "", nil
		}
		return lines[n-1], nil
	})
	rt.RegisterIdent("font", func(c *interp.Call) (string, error) {
		fonts := []string{"Go", "Go Mono", "Arial", "Courier New", "Fixedsys", "Tahoma", "Times New Roman", "Verdana"}
		if !c.Paren {
			return fonts[0], nil
		}
		if n, ok := interp.ParseNumber(c.Arg(0)); ok {
			i := int(n)
			if i == 0 {
				return strconv.Itoa(len(fonts)), nil
			}
			if i >= 1 && i <= len(fonts) {
				return fonts[i-1], nil
			}
			return "", nil
		}
		for _, f := range fonts {
			if strings.EqualFold(f, c.Arg(0)) {
				return f, nil
			}
		}
		return "", nil
	})
}

func argInt(c *interp.Call, i int) int { return interp.Int(c.Arg(i)) }

func rectArgs(c *interp.Call, i int) image.Rectangle {
	x, y, w, h := argInt(c, i), argInt(c, i+1), argInt(c, i+2), argInt(c, i+3)
	return image.Rect(x, y, x+w, y+h)
}

func rect(f []string) image.Rectangle {
	x, y, w, h := interp.Int(f[0]), interp.Int(f[1]), interp.Int(f[2]), interp.Int(f[3])
	return image.Rect(x, y, x+w, y+h)
}

func points(args []string) [][2]float64 {
	var out [][2]float64
	for i := 0; i+1 < len(args); i += 2 {
		out = append(out, [2]float64{interp.Num(args[i]), interp.Num(args[i+1])})
	}
	return out
}

// inPolygon is the even-odd point-in-polygon test.
func inPolygon(x, y float64, pts [][2]float64) bool {
	in := false
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		a, b := pts[i], pts[j]
		if (a[1] > y) != (b[1] > y) && x < (b[0]-a[0])*(y-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

// polygonsOverlap reports whether two polygons intersect: an edge crosses
// or one contains a vertex of the other.
func polygonsOverlap(a, b [][2]float64) bool {
	for _, p := range a {
		if inPolygon(p[0], p[1], b) {
			return true
		}
	}
	for _, p := range b {
		if inPolygon(p[0], p[1], a) {
			return true
		}
	}
	cross := func(o, p, q [2]float64) float64 { return (p[0]-o[0])*(q[1]-o[1]) - (p[1]-o[1])*(q[0]-o[0]) }
	for i := range a {
		a1, a2 := a[i], a[(i+1)%len(a)]
		for j := range b {
			b1, b2 := b[j], b[(j+1)%len(b)]
			if cross(a1, a2, b1)*cross(a1, a2, b2) < 0 && cross(b1, b2, a1)*cross(b1, b2, a2) < 0 {
				return true
			}
		}
	}
	return false
}

// identRGB implements $rgb(r,g,b), $rgb(N) and $rgb(system colour name).
func identRGB(c *interp.Call) (string, error) {
	if len(c.Args) >= 3 {
		return strconv.Itoa(rgbValue(color.RGBA{uint8(argInt(c, 0)), uint8(argInt(c, 1)), uint8(argInt(c, 2)), 255})), nil
	}
	if n, ok := interp.ParseNumber(c.Arg(0)); ok {
		v := int(n)
		return strconv.Itoa(v&0xff) + "," + strconv.Itoa(v>>8&0xff) + "," + strconv.Itoa(v>>16&0xff), nil
	}
	sys := map[string]color.RGBA{
		"face": {240, 240, 240, 255}, "shadow": {160, 160, 160, 255}, "hilight": {255, 255, 255, 255},
		"3dlight": {227, 227, 227, 255}, "frame": {100, 100, 100, 255}, "text": {0, 0, 0, 255},
	}
	if col, ok := sys[strings.ToLower(c.Arg(0))]; ok {
		return strconv.Itoa(rgbValue(col)), nil
	}
	return "", nil
}

// pointsArgs reads x y pairs.
func pointsArgs(f []string) []image.Point {
	var out []image.Point
	for i := 0; i+1 < len(f); i += 2 {
		out = append(out, image.Pt(interp.Int(f[i]), interp.Int(f[i+1])))
	}
	return out
}

func (u *UI) drawDot(c *interp.CmdCall, w *Window, sw string, f []string) error {
	if len(f) < 4 {
		return c.InvalidParams()
	}
	col, size := toColor(f[0], strings.Contains(sw, "r")), interp.Int(f[1])
	cv := canvas{w.Pic, strings.Contains(sw, "i")}
	for _, p := range pointsArgs(f[2:]) {
		cv.dot(p.X, p.Y, size, col)
	}
	return nil
}

func (u *UI) drawLine(c *interp.CmdCall, w *Window, sw string, f []string) error {
	if len(f) < 6 {
		return c.InvalidParams()
	}
	col, size := toColor(f[0], strings.Contains(sw, "r")), interp.Int(f[1])
	cv := canvas{w.Pic, strings.Contains(sw, "i")}
	pts := pointsArgs(f[2:])
	for i := 1; i < len(pts); i++ {
		cv.line(pts[i-1].X, pts[i-1].Y, pts[i].X, pts[i].Y, size, col)
	}
	return nil
}

func (u *UI) drawRect(c *interp.CmdCall, w *Window, sw string, f []string) error {
	if len(f) < 6 {
		return c.InvalidParams()
	}
	col, size := toColor(f[0], strings.Contains(sw, "r")), interp.Int(f[1])
	cv := canvas{w.Pic, strings.Contains(sw, "i")}
	round := strings.Contains(sw, "d")
	step := 4
	if round {
		step = 6
	}
	for i := 2; i+3 < len(f); i += step {
		r := rect(f[i : i+4])
		cw, ch := 0, 0
		if round && i+5 < len(f) {
			cw, ch = interp.Int(f[i+4]), interp.Int(f[i+5])
		}
		if strings.Contains(sw, "c") {
			for x := r.Min.X; x < r.Max.X; x += 2 {
				cv.set(x, r.Min.Y, col)
				cv.set(x, r.Max.Y-1, col)
			}
			for y := r.Min.Y; y < r.Max.Y; y += 2 {
				cv.set(r.Min.X, y, col)
				cv.set(r.Max.X-1, y, col)
			}
			continue
		}
		cv.shape(r, size, col, strings.Contains(sw, "f"), strings.Contains(sw, "e"), round, cw, ch)
	}
	return nil
}

func (u *UI) drawFill(c *interp.CmdCall, w *Window, sw string, f []string) error {
	if len(f) < 4 {
		return c.InvalidParams()
	}
	rgb := strings.Contains(sw, "r")
	col, other := toColor(f[0], rgb), toColor(f[1], rgb)
	cv := canvas{w.Pic, false}
	for _, p := range pointsArgs(f[2:]) {
		cv.flood(p.X, p.Y, col, other, strings.Contains(sw, "s"))
	}
	return nil
}

// drawText implements /drawtext -dhnrpbocv @ <color> [color] [font size] <x y [w h]> <text>.
func (u *UI) drawText(c *interp.CmdCall, w *Window, sw string, f []string) error {
	rgb := strings.Contains(sw, "r")
	if len(f) < 4 {
		return c.InvalidParams()
	}
	fg := toColor(f[0], rgb)
	f = f[1:]
	var bg *color.RGBA
	if strings.Contains(sw, "b") {
		b := toColor(f[0], rgb)
		bg, f = &b, f[1:]
	}
	fontName, size := w.Font, w.FontSize
	if strings.HasPrefix(f[0], "\"") {
		// A quoted font name may contain spaces: "Segoe UI".
		for i, word := range f {
			if strings.HasSuffix(word, "\"") && (i > 0 || len(word) > 1) {
				name := strings.Trim(strings.Join(f[:i+1], " "), "\"")
				f = append([]string{name}, f[i+1:]...)
				break
			}
		}
	}
	if _, ok := interp.ParseNumber(f[0]); !ok && len(f) > 3 {
		fontName, size, f = f[0], interp.Int(f[1]), f[2:]
	}
	if len(f) < 3 {
		return c.InvalidParams()
	}
	x, y := interp.Int(f[0]), interp.Int(f[1])
	f = f[2:]
	var clip image.Rectangle
	if strings.Contains(sw, "c") && len(f) >= 3 {
		clip = image.Rect(x, y, x+interp.Int(f[0]), y+interp.Int(f[1]))
		f = f[2:]
	}
	text := strings.Join(f, " ")
	if strings.Contains(sw, "v") {
		if d := data.From(u.rt); d != nil {
			b, _ := d.Bin(strings.TrimSpace(text))
			text = string(b)
		}
	}
	if strings.Contains(sw, "p") {
		text = stripControl(text)
	}
	if !strings.Contains(sw, "d") {
		text = strings.ReplaceAll(text, "\t", "    ")
	}
	face := Face(fontName, size, strings.Contains(sw, "o"))
	canvas{w.Pic, false}.text(face, x, y, text, fg, bg, clip)
	return nil
}

func resizeCanvas(w *Window, width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	old := w.Pic
	w.Pic = newCanvas(width, height)
	if old != nil {
		draw.Draw(w.Pic, old.Rect, old, image.Point{}, draw.Src)
	}
}

// drawScroll implements /drawscroll -hn @ <dx> <dy> <x y w h> [...].
func (u *UI) drawScroll(c *interp.CmdCall, w *Window, sw string, f []string) error {
	for i := 0; i+5 < len(f); i += 6 {
		dx, dy := interp.Int(f[i]), interp.Int(f[i+1])
		r := rect(f[i+2 : i+6]).Intersect(w.Pic.Rect)
		src := image.NewRGBA(r)
		draw.Draw(src, r, w.Pic, r.Min, draw.Src)
		dst := r.Add(image.Pt(dx, dy)).Intersect(r)
		draw.Draw(w.Pic, dst, src, dst.Min.Sub(image.Pt(dx, dy)), draw.Src)
	}
	return nil
}

// drawCopy implements /drawcopy -ihmnt @ [color] <x y w h> @dest <x y [w h]>.
func (u *UI) drawCopy(c *interp.CmdCall, w *Window, sw string, f []string) error {
	var trans *color.RGBA
	if strings.Contains(sw, "t") && len(f) > 0 {
		t := toColor(f[0], true)
		trans, f = &t, f[1:]
	}
	if len(f) < 7 {
		return c.InvalidParams()
	}
	src := rect(f[:4])
	dst := u.window(f[4])
	if dst == nil || dst.Pic == nil {
		return c.Errorf("'%s' not a picture window", f[4])
	}
	x, y := interp.Int(f[5]), interp.Int(f[6])
	target := image.Rect(x, y, x+src.Dx(), y+src.Dy())
	if len(f) >= 9 {
		target = image.Rect(x, y, x+interp.Int(f[7]), y+interp.Int(f[8]))
	}
	piece := image.NewRGBA(image.Rect(0, 0, src.Dx(), src.Dy()))
	draw.Draw(piece, piece.Rect, w.Pic, src.Min, draw.Src)
	blit(dst.Pic, target, piece, trans, strings.Contains(sw, "m"))
	if !strings.Contains(sw, "n") && dst != w {
		u.Front.WindowChanged(dst)
	}
	return nil
}

// blit scales src into r of dst, skipping a transparent colour.
func blit(dst *image.RGBA, r image.Rectangle, src image.Image, trans *color.RGBA, smooth bool) {
	scaled := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	interp := xdraw.Interpolator(xdraw.NearestNeighbor)
	if smooth {
		interp = xdraw.CatmullRom
	}
	interp.Scale(scaled, scaled.Rect, src, src.Bounds(), xdraw.Src, nil)
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			p := scaled.RGBAAt(x, y)
			if trans != nil && p.R == trans.R && p.G == trans.G && p.B == trans.B {
				continue
			}
			if pt := image.Pt(r.Min.X+x, r.Min.Y+y); pt.In(dst.Rect) {
				dst.SetRGBA(pt.X, pt.Y, p)
			}
		}
	}
}

// loadImage reads a picture file or &binvar.
func (u *UI) loadImage(name string, binvar bool) (image.Image, error) {
	var raw []byte
	if binvar {
		if d := data.From(u.rt); d != nil {
			raw, _ = d.Bin(name)
		}
	} else {
		path := name
		if st := storage.From(u.rt); st != nil {
			path = st.Path(name)
		}
		var err error
		if raw, err = os.ReadFile(path); err != nil {
			return nil, err
		}
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		img, err = bmp.Decode(bytes.NewReader(raw))
	}
	return img, err
}

// drawPic implements /drawpic -ifhmnotscplgNv @ [color] <x y [w h]> [x y w h] [N] [M] <file|&binvar>.
func (u *UI) drawPic(c *interp.CmdCall, w *Window, sw string, f []string) error {
	if len(f) == 0 {
		return nil // /drawpic -c alone clears the cache
	}
	var trans *color.RGBA
	if strings.Contains(sw, "t") {
		t := toColor(f[0], true)
		trans, f = &t, f[1:]
	}
	var nums []int
	for len(f) > 1 {
		v, ok := interp.ParseNumber(f[0])
		if !ok {
			break
		}
		nums, f = append(nums, int(v)), f[1:]
	}
	if len(nums) < 2 || len(f) == 0 {
		return c.InvalidParams()
	}
	img, err := u.loadImage(strings.Trim(strings.Join(f, " "), `"`), strings.Contains(sw, "v"))
	if err != nil {
		return c.Errorf("unable to load '%s'", strings.Join(f, " "))
	}
	b := img.Bounds()
	x, y := nums[0], nums[1]
	target := image.Rect(x, y, x+b.Dx(), y+b.Dy())
	rest := nums[2:]
	if strings.Contains(sw, "s") && len(rest) >= 2 {
		target = image.Rect(x, y, x+rest[0], y+rest[1])
		rest = rest[2:]
	}
	if len(rest) >= 4 {
		part := image.Rect(rest[0], rest[1], rest[0]+rest[2], rest[1]+rest[3]).Add(b.Min)
		sub := image.NewRGBA(image.Rect(0, 0, part.Dx(), part.Dy()))
		draw.Draw(sub, sub.Rect, img, part.Min, draw.Src)
		img = sub
		if !strings.Contains(sw, "s") {
			target = image.Rect(x, y, x+part.Dx(), y+part.Dy())
		}
	}
	if strings.Contains(sw, "l") {
		for ty := y; ty < w.Pic.Rect.Max.Y; ty += target.Dy() {
			for tx := x; tx < w.Pic.Rect.Max.X; tx += target.Dx() {
				blit(w.Pic, image.Rect(tx, ty, tx+target.Dx(), ty+target.Dy()), img, trans, strings.Contains(sw, "m"))
			}
		}
		return nil
	}
	blit(w.Pic, target, img, trans, strings.Contains(sw, "m"))
	return nil
}

// drawSave implements /drawsave -abNqNvN @ [x y w h] <file|&binvar>.
func (u *UI) drawSave(c *interp.CmdCall, w *Window, sw string, f []string) error {
	var img image.Image = w.Pic
	if strings.Contains(sw, "a") && len(f) >= 5 {
		r := rect(f[:4]).Intersect(w.Pic.Rect)
		sub := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		draw.Draw(sub, sub.Rect, w.Pic, r.Min, draw.Src)
		img, f = sub, f[4:]
	}
	target := strings.Trim(strings.Join(f, " "), `"`)
	format := strings.ToLower(strings.TrimPrefix(fileExt(target), "."))
	if i := strings.IndexByte(sw, 'v'); i >= 0 {
		format = map[byte]string{'p': "png", 'g': "gif", 'j': "jpg"}[nextByte(sw, i)]
		if format == "" {
			format = "bmp"
		}
	}
	quality := 90
	if i := strings.IndexByte(sw, 'q'); i >= 0 {
		quality = max(1, min(100, interp.Int(sw[i+1:])))
	}
	var buf bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buf, img)
	case "jpg", "jpeg":
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	case "gif":
		err = gif.Encode(&buf, img, nil)
	default:
		err = bmp.Encode(&buf, img)
	}
	if err != nil {
		return c.Errorf("%s", err.Error())
	}
	if strings.Contains(sw, "v") {
		if d := data.From(u.rt); d != nil {
			d.SetBin(target, buf.Bytes())
		}
		return nil
	}
	path := target
	if st := storage.From(u.rt); st != nil {
		path = st.Path(target)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func fileExt(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		return p[i:]
	}
	return ""
}

func nextByte(s string, i int) byte {
	if i+1 < len(s) {
		return s[i+1]
	}
	return 0
}

// drawRot implements /drawrot -hmnpbfc @ [color] <angle> [x y w h].
func (u *UI) drawRot(c *interp.CmdCall, w *Window, sw string, f []string) error {
	bg := color.RGBA{255, 255, 255, 255}
	if strings.Contains(sw, "b") && len(f) > 0 {
		bg, f = toColor(f[0], true), f[1:]
	}
	if len(f) == 0 {
		return c.InvalidParams()
	}
	angle := interp.Num(f[0]) * math.Pi / 180
	r := w.Pic.Rect
	if len(f) >= 5 {
		r = rect(f[1:5]).Intersect(w.Pic.Rect)
	}
	src := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(src, src.Rect, w.Pic, r.Min, draw.Src)
	cx, cy := float64(r.Dx())/2, float64(r.Dy())/2
	sin, cos := math.Sin(angle), math.Cos(angle)
	m := f64.Aff3{cos, -sin, cx - cx*cos + cy*sin, sin, cos, cy - cx*sin - cy*cos}
	out := image.NewRGBA(src.Rect)
	draw.Draw(out, out.Rect, image.NewUniform(bg), image.Point{}, draw.Src)
	xdraw.BiLinear.Transform(out, m, src, src.Rect, xdraw.Over, nil)
	draw.Draw(w.Pic, r, out, image.Point{}, draw.Src)
	return nil
}

// identClick implements $click(@,N).x|y over the window's click history.
func (u *UI) identClick(c *interp.Call) (string, error) {
	clicks := u.clicks[strings.ToLower(c.Arg(0))]
	n := argInt(c, 1)
	if n == 0 {
		return strconv.Itoa(len(clicks)), nil
	}
	if n < 1 || n > len(clicks) {
		return "", nil
	}
	p := clicks[n-1]
	switch strings.ToLower(c.Prop) {
	case "x":
		return strconv.Itoa(p.X), nil
	case "y":
		return strconv.Itoa(p.Y), nil
	}
	return strconv.Itoa(p.X) + " " + strconv.Itoa(p.Y), nil
}

// identPic implements $pic(file).size|width|height|frames.
func (u *UI) identPic(c *interp.Call) (string, error) {
	path := c.Arg(0)
	if st := storage.From(u.rt); st != nil {
		path = st.Path(path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		if cfg, err = bmp.DecodeConfig(bytes.NewReader(raw)); err != nil {
			return "", nil
		}
	}
	switch strings.ToLower(c.Prop) {
	case "width":
		return strconv.Itoa(cfg.Width), nil
	case "height":
		return strconv.Itoa(cfg.Height), nil
	case "frames":
		if g, err := gif.DecodeAll(bytes.NewReader(raw)); err == nil {
			return strconv.Itoa(len(g.Image)), nil
		}
		return "1", nil
	case "delay":
		if g, err := gif.DecodeAll(bytes.NewReader(raw)); err == nil && argInt(c, 1) >= 1 && argInt(c, 1) <= len(g.Delay) {
			return strconv.Itoa(g.Delay[argInt(c, 1)-1] * 10), nil
		}
		return "0", nil
	case "icons":
		return "0", nil
	}
	return strconv.Itoa(len(raw)), nil
}
