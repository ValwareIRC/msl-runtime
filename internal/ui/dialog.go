// Package ui holds the state of script-created user interface: dialogs and
// custom @windows. The model lives on the runtime goroutine and works with no
// display; a Frontend (such as the Gio GUI) renders it and reports user input.
package ui

import (
	"strconv"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

// Control is one control in a dialog.
type Control struct {
	ID               int
	Kind             string // text edit button check radio box scroll list combo icon link tab menu item
	Text             string
	Lines            []string // list, combo and multi-line edit contents
	Sel              []int    // selected line numbers (1-based)
	Checked          []bool   // per-line check state for list check style
	State            int      // 0 off, 1 on, 2 indeterminate (check, radio)
	X, Y             int
	W, H             int
	Styles           map[string]string
	Visible          bool
	Enabled          bool
	Edited           bool
	Tab              int // owning tab id, 0 for none
	Var              string
	Min              int // scroll range
	Max              int
	Pos              int
	Menu             int // parent menu id for menu/item
	File             string
	SelStart, SelEnd int
}

// Has reports whether the control has a style word.
func (c *Control) Has(style string) bool { _, ok := c.Styles[style]; return ok }

// Dialog is an open dialog.
type Dialog struct {
	Name     string
	Table    string
	Title    string
	X, Y     int
	W, H     int
	Unit     string // dbu, map or pixels
	Modal    bool
	OnTop    bool
	Controls []*Control
	Focus    int
	Ok       int
	Cancel   int
	Result   int
	Active   bool
	Cid      int

	file   *script.File
	closed bool
	okHit  bool
	byID   map[int]*Control
}

// Control returns a control by id.
func (d *Dialog) Control(id int) *Control { return d.byID[id] }

// Tab returns the id of the selected tab, or 0.
func (d *Dialog) Tab() int {
	for _, c := range d.Controls {
		if c.Kind == "tab" && c.State == 1 {
			return c.ID
		}
	}
	return 0
}

// splitFields splits a dialog table line on commas outside quotes.
func splitFields(s string) []string {
	var out []string
	var b strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			b.WriteRune(r)
		case r == ',' && !quoted:
			out = append(out, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	return append(out, strings.TrimSpace(b.String()))
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func ints(s string) []int {
	var out []int
	for _, f := range strings.Fields(s) {
		out = append(out, interp.Int(f))
	}
	for len(out) < 4 {
		out = append(out, -1)
	}
	return out
}

// textKinds are controls whose first field is their text.
var textKinds = map[string]bool{
	"text": true, "edit": true, "button": true, "check": true, "radio": true,
	"box": true, "scroll": true, "link": true, "tab": true, "menu": true, "item": true,
}

// buildDialog creates a dialog from a table. Each line is evaluated first,
// so identifiers in tables use their values at creation time.
func buildDialog(f *interp.Frame, name string, def *script.Dialog) (*Dialog, error) {
	d := &Dialog{Name: name, Table: def.Name, X: -1, Y: -1, W: -1, H: -1, Unit: "pixels",
		file: def.File, byID: map[int]*Control{}}
	lastMenu, lastTab := 0, 0
	for _, raw := range def.Lines {
		kind, rest, _ := strings.Cut(strings.TrimSpace(raw), " ")
		kind = strings.ToLower(kind)
		line, err := evalTableLine(f, rest)
		if err != nil {
			return nil, err
		}
		switch kind {
		case "title":
			d.Title = unquote(line)
			continue
		case "size":
			v := ints(line)
			d.X, d.Y, d.W, d.H = v[0], v[1], v[2], v[3]
			continue
		case "option":
			for _, o := range strings.Fields(line) {
				switch strings.ToLower(o) {
				case "dbu", "map", "pixels":
					d.Unit = strings.ToLower(o)
				}
			}
			continue
		}
		fields := splitFields(line)
		c := &Control{Kind: kind, Visible: true, Enabled: true, Styles: map[string]string{}, Max: 100,
			X: -1, Y: -1, W: -1, H: -1}
		i := 0
		if textKinds[kind] {
			c.Text = unquote(fields[0])
			i = 1
		}
		if i < len(fields) {
			c.ID = interp.Int(fields[i])
			i++
		}
		switch kind {
		case "menu":
			c.Menu = 0
			if i < len(fields) {
				c.Menu = interp.Int(fields[i])
			}
			lastMenu = c.ID
		case "item":
			c.Menu = lastMenu
			if i < len(fields) && fields[i] != "" {
				if n, ok := interp.ParseNumber(fields[i]); ok {
					c.Menu = int(n)
					lastMenu = c.Menu
					i++
				}
			}
			if strings.EqualFold(c.Text, "break") {
				c.Kind = "break"
			}
		default:
			if i < len(fields) {
				v := ints(fields[i])
				c.X, c.Y, c.W, c.H = v[0], v[1], v[2], v[3]
				i++
			}
		}
		if kind == "icon" && i < len(fields) {
			c.File = unquote(fields[i])
			i++
			if i < len(fields) {
				if _, ok := interp.ParseNumber(fields[i]); ok {
					i++
				}
			}
		}
		for ; i < len(fields); i++ {
			parseStyles(c, fields[i])
		}
		if kind == "tab" {
			if c.X < 0 && lastTab != 0 {
				c.Tab = lastTab // an extra tab page of the first tab control
			} else {
				lastTab = c.ID
			}
			if len(d.tabs()) == 0 {
				c.State = 1
			}
		}
		if c.Has("ok") {
			d.Ok = c.ID
		}
		if c.Has("cancel") {
			d.Cancel = c.ID
		}
		if c.Has("result") {
			d.Result = c.ID
		}
		if c.Has("disable") {
			c.Enabled = false
		}
		if c.Has("hide") {
			c.Visible = false
		}
		if kind == "check" || kind == "radio" {
			c.State = 0
		}
		if kind == "edit" && strings.Contains(c.Text, "\n") {
			c.Lines = strings.Split(c.Text, "\n")
		}
		d.Controls = append(d.Controls, c)
		d.byID[c.ID] = c
	}
	return d, nil
}

func (d *Dialog) tabs() []*Control {
	var out []*Control
	for _, c := range d.Controls {
		if c.Kind == "tab" {
			out = append(out, c)
		}
	}
	return out
}

// evalTableLine evaluates a dialog table line field by field, leaving
// %variable words alone so they stay bindings rather than values.
func evalTableLine(f *interp.Frame, rest string) (string, error) {
	fields := splitFields(rest)
	for i, field := range fields {
		words := strings.Fields(field)
		for j, w := range words {
			if strings.HasPrefix(w, "%") && i > 0 {
				continue
			}
			v, err := f.Eval(w)
			if err != nil {
				return "", err
			}
			words[j] = v
		}
		fields[i] = strings.Join(words, " ")
	}
	return strings.Join(fields, ", "), nil
}

// parseStyles reads a style field such as "ok tab 1 limit 20 %var".
func parseStyles(c *Control, field string) {
	words := strings.Fields(field)
	for k := 0; k < len(words); k++ {
		w := strings.ToLower(words[k])
		switch {
		case strings.HasPrefix(words[k], "%"):
			c.Var = words[k]
		case w == "tab" && k+1 < len(words):
			c.Tab = interp.Int(words[k+1])
			k++
		case w == "limit" && k+1 < len(words):
			c.Styles["limit"] = words[k+1]
			k++
		case (w == "pos" || w == "page") && k+1 < len(words):
			c.Styles[w] = words[k+1]
			if w == "pos" {
				c.Pos = interp.Int(words[k+1])
			}
			k++
		case w == "range" && k+2 < len(words):
			c.Min, c.Max = interp.Int(words[k+1]), interp.Int(words[k+2])
			k += 2
		default:
			c.Styles[w] = ""
		}
	}
}

// Value returns a control's value for the dialog result and %var bindings.
func (c *Control) Value() string {
	switch c.Kind {
	case "check", "radio":
		return strconv.Itoa(c.State)
	case "list", "combo":
		if len(c.Sel) > 0 && c.Sel[0] >= 1 && c.Sel[0] <= len(c.Lines) {
			return c.Lines[c.Sel[0]-1]
		}
		if c.Kind == "combo" {
			return c.Text
		}
		return ""
	case "edit":
		if len(c.Lines) > 0 {
			return strings.Join(c.Lines, " ")
		}
	case "scroll":
		return strconv.Itoa(c.Pos)
	}
	return c.Text
}

// matchIDs reports whether id is in a list like "1-3,5,7".
func matchIDs(list string, id int) bool {
	if list == "*" || list == "" {
		return true
	}
	for _, part := range strings.Split(list, ",") {
		a, b, ok := strings.Cut(strings.TrimSpace(part), "-")
		if !ok {
			if interp.Int(a) == id {
				return true
			}
			continue
		}
		if id >= interp.Int(a) && id <= interp.Int(b) {
			return true
		}
	}
	return false
}

// parseIDs expands "2,12-16,20" into ids.
func parseIDs(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		a, b, ok := strings.Cut(strings.TrimSpace(part), "-")
		if !ok {
			out = append(out, interp.Int(a))
			continue
		}
		for i := interp.Int(a); i <= interp.Int(b); i++ {
			out = append(out, i)
		}
	}
	return out
}
