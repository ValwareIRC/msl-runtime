package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// iconICO draws the .mrc file icon, a rounded square with "m", and returns
// it as a Windows .ico holding a 256x256 PNG.
func iconICO() ([]byte, error) {
	const size = 256
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	bg := color.NRGBA{88, 101, 242, 255}
	r := 56.0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if inRounded(float64(x)+0.5, float64(y)+0.5, 8, 248, r) {
				img.SetNRGBA(x, y, bg)
			}
		}
	}
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 150, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	d := font.Drawer{Dst: img, Src: image.NewUniform(color.White), Face: face}
	w := d.MeasureString("m")
	d.Dot = fixed.Point26_6{X: (fixed.I(size) - w) / 2, Y: fixed.I(176)}
	d.DrawString("m")

	var pngData bytes.Buffer
	if err := png.Encode(&pngData, img); err != nil {
		return nil, err
	}
	var ico bytes.Buffer
	// ICONDIR, one ICONDIRENTRY (0 means 256), then the PNG.
	binary.Write(&ico, binary.LittleEndian, []uint16{0, 1, 1})
	ico.Write([]byte{0, 0, 0, 0})
	binary.Write(&ico, binary.LittleEndian, []uint16{1, 32})
	binary.Write(&ico, binary.LittleEndian, []uint32{uint32(pngData.Len()), 22})
	ico.Write(pngData.Bytes())
	return ico.Bytes(), nil
}

// inRounded reports whether a point lies in the square [lo,hi] with corner radius r.
func inRounded(x, y, lo, hi, r float64) bool {
	if x < lo || y < lo || x > hi || y > hi {
		return false
	}
	cx := min(max(x, lo+r), hi-r)
	cy := min(max(y, lo+r), hi-r)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}
