package ui_test

import (
	"strings"
	"testing"

	"github.com/ValwareIRC/msl-runtime/internal/engine"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

func TestPictureWindow(t *testing.T) {
	out := &output{}
	eng := engine.New(out, t.TempDir())
	_, errs := eng.RT.LoadSource("draw.mrc", `
alias draw {
  window -p @p 0 0 120 100
  drawrect -f @p 4 1 10 10 20 20
  drawdot @p 2 3 50 50
  drawline @p 1 1 0 90 119 90
  drawrect -e @p 12 1 60 10 40 30
  drawfill -s @p 9 0 80 25
  drawtext @p 1 Arial 12 5 60 Hi
  echo -s dots $getdot(@p,15,15) $getdot(@p,50,50) $getdot(@p,60,90) $getdot(@p,80,25) $getdot(@p,5,5)
  echo -s rgb $rgb(255,0,0) $rgb(255) $rgb(16711680)
  echo -s geo $inrect(5,5,0,0,10,10) $inrect(15,5,0,0,10,10) $inellipse(10,10,0,0,20,20) $inellipse(1,1,0,0,20,20) $inpoly(5,5,0,0,10,0,10,10,0,10) $onpoly(3,3,0,0,10,0,5,10,5,5,15,5,10,15)
  echo -s text $iif($width(Hello,Arial,12) > 20,wide) $iif($height(Hello,Arial,12) > 10,tall) $wrap(one two three four,Arial,12,40,1,0)
  drawsave @p shot.png
  echo -s pic $pic(shot.png).width $pic(shot.png).height
  drawreplace @p 4 3 10 10 20 20
  echo -s replaced $getdot(@p,15,15)
}
`, script.Remote)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	eng.RT.Exec("draw")
	got := strings.Join(out.lines, "\n")
	want := strings.Join([]string{
		"dots 255 8323072 0 64512 16777215",
		"rgb 255 255,0,0 0,0,255",
		"geo $true $false $true $false $true $true",
		"text wide tall 4", // Go font metrics, not Windows fonts
		"pic 120 100",
		"replaced 37632",
	}, "\n")
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	// Some text pixels were drawn in black.
	w := eng.UI.Window("@p")
	dark := 0
	for y := 60; y < 80; y++ {
		for x := 5; x < 30; x++ {
			if c := w.Pic.RGBAAt(x, y); c.R < 100 && c.G < 100 && c.B < 100 {
				dark++
			}
		}
	}
	if dark == 0 {
		t.Error("drawtext drew nothing")
	}
}
