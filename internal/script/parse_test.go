package script

import (
	"fmt"
	"strings"
	"testing"
)

// dump renders a block compactly for comparison in tests.
func dump(b Block) string {
	var parts []string
	for _, s := range b {
		switch s := s.(type) {
		case *Cmd:
			parts = append(parts, "cmd("+s.Text+")")
		case *Label:
			parts = append(parts, "label("+s.Name+")")
		case *While:
			parts = append(parts, fmt.Sprintf("while[%s]{%s}", s.Cond, dump(s.Body)))
		case *Nested:
			parts = append(parts, "{"+dump(s.Body)+"}")
		case *If:
			var bs []string
			for _, br := range s.Branches {
				bs = append(bs, fmt.Sprintf("[%s]{%s}", br.Cond, dump(br.Body)))
			}
			out := "if" + strings.Join(bs, "elseif")
			if s.HasElse {
				out += "else{" + dump(s.Else) + "}"
			}
			parts = append(parts, out)
		}
	}
	return strings.Join(parts, "; ")
}

func TestParseRemote(t *testing.T) {
	src := `; a comment
/*
block comment
*/
alias -l add { return $calc($1 + $2) }
alias greet {
  if ($1 == hi) { echo -s hello }
  elseif ($1 == bye) echo -s goodbye
  else {
    echo -s what $&
      is that
  }
  :done
}
#auto on
on *:TEXT:*hello*:#:{ msg # hi $nick | halt }
#auto end
on $^*:TEXT:/^!(\w+):x$/i:?:echo -s regex
raw 332:*:echo -s topic $3-
ctcp *:PING:*:ctcpreply $nick PING $2
on me:*:JOIN:#:echo -s joined
dialog -l test {
  title "Test"
  size -1 -1 100 50
}
menu @win {
  sclick:echo -s click
}
`
	f, errs := Parse("t.mrc", src, Remote)
	for _, e := range errs {
		t.Error(e)
	}
	if len(f.Aliases) != 2 || !f.Aliases[0].Local || f.Aliases[0].Name != "add" {
		t.Fatalf("aliases = %+v", f.Aliases)
	}
	if got, want := dump(f.Aliases[0].Body), "cmd(return $calc($1 + $2))"; got != want {
		t.Errorf("add body = %s, want %s", got, want)
	}
	want := "if[($1 == hi)]{cmd(echo -s hello)}elseif[($1 == bye)]{cmd(echo -s goodbye)}else{cmd(echo -s what is that)}; label(done)"
	if got := dump(f.Aliases[1].Body); got != want {
		t.Errorf("greet body =\n %s\nwant\n %s", got, want)
	}

	if len(f.Events) != 5 {
		t.Fatalf("events = %d, want 5", len(f.Events))
	}
	text := f.Events[0]
	if text.Name != "TEXT" || text.Level != "*" || text.Group == nil || !text.Group.On ||
		strings.Join(text.Fields, "|") != "*hello*|#" {
		t.Errorf("text event = %+v", text)
	}
	if got := dump(text.Body); got != "cmd(msg # hi $nick); cmd(halt)" {
		t.Errorf("text body = %s", got)
	}
	re := f.Events[1]
	if re.Prefixes != "$^" || re.Fields[0] != `/^!(\w+):x$/i` || re.Fields[1] != "?" {
		t.Errorf("regex event = %+v", re)
	}
	if raw := f.Events[2]; raw.Kind != "raw" || raw.Name != "332" || raw.Fields[0] != "*" {
		t.Errorf("raw event = %+v", raw)
	}
	if ctcp := f.Events[3]; ctcp.Kind != "ctcp" || strings.Join(ctcp.Fields, "|") != "PING|*" {
		t.Errorf("ctcp event = %+v", ctcp)
	}
	if len(f.Dialogs) != 1 || f.Dialogs[0].Name != "test" || len(f.Dialogs[0].Lines) != 2 {
		t.Errorf("dialogs = %+v", f.Dialogs)
	}
	if len(f.Menus) != 1 || f.Menus[0].Targets[0] != "@win" {
		t.Errorf("menus = %+v", f.Menus)
	}
}

func TestMeEvent(t *testing.T) {
	f, errs := Parse("t.mrc", "on me:*:JOIN:#:echo -s joined", Remote)
	if len(errs) > 0 || len(f.Events) != 1 || !f.Events[0].Me || f.Events[0].Name != "JOIN" {
		t.Fatalf("me event = %+v, %v", f.Events, errs)
	}
}

func TestBlocks(t *testing.T) {
	for src, want := range map[string]string{
		"if (%x) echo a | echo b":                           "if[(%x)]{cmd(echo a)}; cmd(echo b)",
		"if (%a == 1) && (%b == 2) echo y":                  "if[(%a == 1) && (%b == 2)]{cmd(echo y)}",
		"if %nick == $null goto done":                       "if[%nick == $null]{cmd(goto done)}",
		"if %x { echo a }":                                  "if[%x]{cmd(echo a)}",
		"if (a) if (b) { echo c }":                          "if[(a)]{if[(b)]{cmd(echo c)}}",
		"while (%i < 3) { inc %i }":                         "while[(%i < 3)]{cmd(inc %i)}",
		"if ($len($1) > 3) { echo long } | else echo short": "if[($len($1) > 3)]{cmd(echo long)}else{cmd(echo short)}",
		"{ echo nested }":                                   "{cmd(echo nested)}",
	} {
		var errs []error
		got := dump(parseBlock("t", []line{{1, src}}, &errs))
		if len(errs) > 0 || got != want {
			t.Errorf("%s\n got %s %v\nwant %s", src, got, errs, want)
		}
	}
}

func TestBraceOnNextLine(t *testing.T) {
	f, errs := Parse("t.mrc", "alias x {\n  if (1)\n  {\n    echo y\n  }\n}", Remote)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := dump(f.Aliases[0].Body); got != "if[(1)]{cmd(echo y)}" {
		t.Errorf("got %s", got)
	}
}

func TestINIAndAliasesFile(t *testing.T) {
	f, errs := Parse("a.ini", "[aliases]\nn1=/j /join $1\nn0=op mode # +o $1\n", Aliases)
	if len(errs) > 0 || len(f.Aliases) != 2 || f.Aliases[0].Name != "op" || f.Aliases[1].Name != "j" {
		t.Fatalf("aliases = %+v %v", f.Aliases, errs)
	}
}

func TestErrors(t *testing.T) {
	_, errs := Parse("t.mrc", "alias x {\n echo\n", Remote)
	if len(errs) == 0 {
		t.Fatal("want missing } error")
	}
}
