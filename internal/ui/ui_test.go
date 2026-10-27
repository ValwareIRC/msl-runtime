package ui_test

import (
	"strings"
	"testing"

	"github.com/ValwareIRC/msl-runtime/internal/engine"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

type output struct{ lines []string }

func (o *output) Echo(_, text string) { o.lines = append(o.lines, text) }
func (o *output) Error(text string)   { o.lines = append(o.lines, "ERR "+text) }

const src = `
dialog demo {
  title "Demo"
  size -1 -1 200 100
  option dbu
  text "Name:", 1, 5 5 30 10
  edit "bob", 2, 40 5 80 10, autohs %dname
  list 3, 5 20 100 50, sort
  button "OK", 4, 5 80 40 12, ok
  button "Cancel", 5, 50 80 40 12, cancel
  check "Agree", 6, 100 80 50 12
  radio "A", 7, 5 95 20 10, group
  radio "B", 8, 30 95 20 10
  menu "&File", 20
  item "&Quit", 21
}
on *:DIALOG:demo:init:0:{
  did -a demo 3 zeta
  did -a demo 3 alpha
  did -c demo 3 1
  echo -s init $did(demo,2) $did(demo,3).lines $did(demo,3).seltext
}
on *:DIALOG:demo:sclick:6-8:{ echo -s click $did $did($did).state }
on *:DIALOG:demo:edit:2:{ echo -s edit $did(2) }
on *:DIALOG:demo:sclick:4:{ echo -s ok clicked }
on *:DIALOG:demo:close:0:{ echo -s closed $dname }
on *:DIALOG:demo:menu:21:{ echo -s menu quit }
alias open { dialog -m demo demo | echo -s open $dialog(0) $dialog(demo).title $dialog(demo).ok }
alias modal { echo -s modal < $+ $dialog(m1,demo) $+ > $dialog(0) }
alias win {
  window -l @w
  aline @w beta
  aline @w alpha
  echo @w hello there
  iline @w 1 first
  echo -s lines $line(@w,0) $line(@w,1) $line(@w,4) $fline(@w,h*,1) $window(@w).type
  sline @w 2
  echo -s sel $sline(@w,0) $sline(@w,1) $sline(@w,1).ln
  dline @w 1-2
  rline 4 @w 1 replaced
  echo -s after $line(@w,0) $line(@w,1) $line(@w,1).color
  window -c @w
  echo -s gone $window(@w)
}
on *:CLOSE:@:{ echo -s closed window $target }
`

func TestDialogsAndWindows(t *testing.T) {
	out := &output{}
	eng := engine.New(out, t.TempDir())
	if _, errs := eng.RT.LoadSource("ui.mrc", src, script.Remote); len(errs) > 0 {
		t.Fatal(errs)
	}
	u := eng.UI
	eng.RT.Exec("open")
	u.Click("demo", 6)
	u.Click("demo", 8)
	u.SetText("demo", 2, "alice")
	u.Click("demo", 21)
	u.Click("demo", 4)
	eng.RT.Exec("echo -s var %dname")
	eng.RT.Exec("modal")
	eng.RT.Exec("win")

	want := []string{
		"init bob 2 alpha",
		"open 1 Demo 4",
		"click 6 1",
		"click 8 1",
		"edit alice",
		"menu quit",
		"ok clicked",
		"closed demo",
		"var alice",
		"modal <> 0",
		"lines 4 first hello there 4 custom",
		"sel 1 beta 2",
		"after 2 replaced 4",
		"closed window @w",
		"gone",
	}
	got := out.lines
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
