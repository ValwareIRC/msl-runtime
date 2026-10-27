package ui

import (
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

// Text is drawn with the system's fonts when the named font is installed,
// so measurements match mIRC on the same machine. Characters a font lacks
// (emoji, symbols) fall back to an emoji font, then to the Go fonts that
// ship with the program. Font names suggesting fixed width, or fonts that
// cannot be found, use Go Mono or Go Regular.
var (
	fontsOnce sync.Once
	goFonts   = map[string]*sfnt.Font{}
	system    = map[string]fontFiles{} // lower-case family name
	parsed    = map[fontRef]*sfnt.Font{}
	faceCache = map[faceKey]font.Face{}
	faceMu    sync.Mutex
)

type fontRef struct {
	path  string
	index int
}

type fontFiles struct{ regular, bold fontRef }

type faceKey struct {
	name string
	bold bool
	size int
}

// emojiFamilies are fallback fonts with outline (not colour bitmap) glyphs.
var emojiFamilies = []string{"segoe ui emoji", "segoe ui symbol", "noto emoji", "symbola", "dejavu sans"}

func loadFonts() {
	for name, data := range map[string][]byte{
		"regular": goregular.TTF, "bold": gobold.TTF, "mono": gomono.TTF, "monobold": gomonobold.TTF,
	} {
		if f, err := opentype.Parse(data); err == nil {
			goFonts[name] = f
		}
	}
	for _, dir := range fontDirs() {
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			switch strings.ToLower(filepath.Ext(p)) {
			case ".ttf", ".otf", ".ttc":
				indexFont(p)
			}
			return nil
		})
	}
}

func fontDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		win := os.Getenv("WINDIR")
		if win == "" {
			win = `C:\Windows`
		}
		return []string{filepath.Join(win, "Fonts"), filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Windows", "Fonts")}
	case "darwin":
		return []string{"/System/Library/Fonts", "/Library/Fonts", filepath.Join(home, "Library", "Fonts")}
	}
	return []string{"/usr/share/fonts", "/usr/local/share/fonts", filepath.Join(home, ".local", "share", "fonts"), filepath.Join(home, ".fonts")}
}

// indexFont records a font file's family and whether it is the regular or bold face.
func indexFont(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	coll, err := sfnt.ParseCollection(data)
	if err != nil {
		return
	}
	var buf sfnt.Buffer
	for i := 0; i < coll.NumFonts(); i++ {
		f, err := coll.Font(i)
		if err != nil {
			continue
		}
		family, err := f.Name(&buf, sfnt.NameIDFamily)
		if err != nil {
			continue
		}
		style, _ := f.Name(&buf, sfnt.NameIDSubfamily)
		key := strings.ToLower(family)
		ff := system[key]
		ref := fontRef{path, i}
		switch strings.ToLower(style) {
		case "regular", "normal", "book", "roman":
			ff.regular = ref
		case "bold":
			ff.bold = ref
		default:
			if ff.regular.path == "" && !strings.Contains(strings.ToLower(style), "italic") {
				ff.regular = ref
			}
		}
		system[key] = ff
	}
}

func openRef(ref fontRef) *sfnt.Font {
	if ref.path == "" {
		return nil
	}
	if f, ok := parsed[ref]; ok {
		return f
	}
	var f *sfnt.Font
	if data, err := os.ReadFile(ref.path); err == nil {
		if coll, err := sfnt.ParseCollection(data); err == nil {
			f, _ = coll.Font(ref.index)
		}
	}
	parsed[ref] = f
	return f
}

// systemFont returns an installed font by family name.
func systemFont(name string, bold bool) *sfnt.Font {
	ff, ok := system[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil
	}
	if bold {
		if f := openRef(ff.bold); f != nil {
			return f
		}
	}
	return openRef(ff.regular)
}

// isMono reports whether a font name asks for a fixed-width face.
func isMono(name string) bool {
	n := strings.ToLower(name)
	for _, m := range []string{"fixedsys", "courier", "mono", "consolas", "terminal", "lucida console"} {
		if strings.Contains(n, m) {
			return true
		}
	}
	return false
}

// Face returns a font face for a font name and point size. A negative size
// is taken as its magnitude, as mIRC's font dialog sizes are.
func Face(name string, size int, bold bool) font.Face {
	if size < 0 {
		size = -size
	}
	if size == 0 {
		size = 9
	}
	key := faceKey{strings.ToLower(name), bold, size}
	faceMu.Lock()
	defer faceMu.Unlock()
	fontsOnce.Do(loadFonts)
	if f, ok := faceCache[key]; ok {
		return f
	}
	fallback := "regular"
	switch {
	case isMono(name) && bold:
		fallback = "monobold"
	case isMono(name):
		fallback = "mono"
	case bold:
		fallback = "bold"
	}
	// The named font sets the metrics; emoji fonts and the Go font fill gaps.
	primary := systemFont(name, bold)
	if primary == nil {
		primary = goFonts[fallback]
	}
	chain := []*sfnt.Font{primary}
	for _, e := range emojiFamilies {
		if f := systemFont(e, false); f != nil {
			chain = append(chain, f)
		}
	}
	if primary != goFonts[fallback] {
		chain = append(chain, goFonts[fallback])
	}
	cf := &chainFace{fonts: chain}
	cf.fonts = nil
	for _, f := range chain {
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: float64(size), DPI: 96, Hinting: font.HintingFull})
		if err != nil {
			continue
		}
		cf.fonts = append(cf.fonts, f)
		cf.faces = append(cf.faces, face)
	}
	faceCache[key] = cf
	return cf
}

// chainFace draws each character with the first font that has a glyph for it.
type chainFace struct {
	mu    sync.Mutex
	fonts []*sfnt.Font
	faces []font.Face
	buf   sfnt.Buffer
}

func (c *chainFace) pick(r rune) font.Face {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, f := range c.fonts {
		if idx, err := f.GlyphIndex(&c.buf, r); err == nil && idx != 0 {
			return c.faces[i]
		}
	}
	return c.faces[0]
}

func (c *chainFace) Close() error { return nil }

func (c *chainFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	return c.pick(r).Glyph(dot, r)
}

func (c *chainFace) GlyphBounds(r rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	return c.pick(r).GlyphBounds(r)
}

func (c *chainFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) { return c.pick(r).GlyphAdvance(r) }

func (c *chainFace) Kern(r0, r1 rune) fixed.Int26_6 {
	f := c.pick(r0)
	if f != c.pick(r1) {
		return 0
	}
	return f.Kern(r0, r1)
}

func (c *chainFace) Metrics() font.Metrics { return c.faces[0].Metrics() }

// TextWidth measures text in pixels.
func TextWidth(face font.Face, text string) int {
	return font.MeasureString(face, text).Ceil()
}

// TextHeight is the line height of a face in pixels.
func TextHeight(face font.Face) int {
	m := face.Metrics()
	return (m.Ascent + m.Descent).Ceil()
}

// fontArgs reads the (text, font, size, bipt) arguments of $width and $height.
func fontArgs(c *interp.Call) (string, font.Face) {
	opts := c.Arg(3)
	bold := strings.Contains(opts, "b") && !strings.Contains(opts, "b0")
	text := c.Arg(0)
	if strings.Contains(opts, "p") && !strings.Contains(opts, "p0") {
		text = stripControl(text)
	}
	return text, Face(c.Arg(1), interp.Int(c.Arg(2)), bold)
}

// stripControl removes formatting codes before measuring.
func stripControl(s string) string {
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

// wrapText splits text into lines no wider than width pixels, breaking at
// spaces when possible.
func wrapText(face font.Face, text string, width int, words bool) []string {
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		cur := ""
		flush := func() { lines = append(lines, cur); cur = "" }
		for _, w := range splitKeep(para, words) {
			if TextWidth(face, cur+w) <= width || cur == "" {
				if cur == "" && TextWidth(face, w) > width {
					for _, r := range w {
						if cur != "" && TextWidth(face, cur+string(r)) > width {
							flush()
						}
						cur += string(r)
					}
					continue
				}
				cur += w
				continue
			}
			flush()
			cur = strings.TrimLeft(w, " ")
		}
		flush()
	}
	return lines
}

func splitKeep(s string, words bool) []string {
	if !words {
		var out []string
		for _, r := range s {
			out = append(out, string(r))
		}
		return out
	}
	var out []string
	for i, w := range strings.Split(s, " ") {
		if i > 0 {
			w = " " + w
		}
		out = append(out, w)
	}
	return out
}
