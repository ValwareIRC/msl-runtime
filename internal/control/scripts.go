package control

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/script"
)

func (c *Control) installScripts() {
	rt := c.rt
	rt.RegisterCommand("load", interp.Command{Fn: func(cc *interp.CmdCall) error { return c.cmdLoad(cc, true) }})
	rt.RegisterCommand("reload", interp.Command{Fn: func(cc *interp.CmdCall) error { return c.cmdLoad(cc, false) }})
	rt.RegisterCommand("unload", interp.Command{Fn: c.cmdUnload})
	rt.RegisterCommand("save", interp.Command{Fn: c.cmdSave})
	rt.RegisterCommand("saveini", interp.Command{Fn: func(cc *interp.CmdCall) error { return c.SaveState() }})
	rt.RegisterCommand("alias", interp.Command{Fn: c.cmdAlias})
	rt.RegisterCommand("enable", interp.Command{Fn: func(cc *interp.CmdCall) error { return c.setGroups(cc, true) }})
	rt.RegisterCommand("disable", interp.Command{Fn: func(cc *interp.CmdCall) error { return c.setGroups(cc, false) }})
	rt.RegisterCommand("groups", interp.Command{Fn: c.cmdGroups})
	rt.RegisterCommand("signal", interp.Command{Fn: c.cmdSignal})
	rt.RegisterIdent("group", c.identGroup)
	rt.RegisterIdent("alias", c.identAlias)
	rt.RegisterIdent("isalias", c.identIsalias)
	rt.RegisterIdent("signal", func(cl *interp.Call) (string, error) { v, _ := cl.F.Event.Get("signal"); return v, nil })
	rt.DeclareEvents("SIGNAL")
}

func (c *Control) path(name string) string {
	if c.st != nil {
		return c.st.Path(name)
	}
	return name
}

func (c *Control) fileByPath(path string, kind script.Kind) *script.File {
	for _, f := range c.rt.Files() {
		if f.Kind == kind && (strings.EqualFold(f.Path, path) || strings.EqualFold(filepath.Base(f.Path), filepath.Base(path))) {
			return f
		}
	}
	return nil
}

// cmdLoad implements /load and /reload <-a|-rs|-ru|-rv> <file>. /load fires
// on LOAD and on START in the loaded script; /reload fires nothing.
func (c *Control) cmdLoad(cc *interp.CmdCall, events bool) error {
	sw, rest := cc.Switches()
	name := strings.Trim(strings.TrimSpace(rest), `"`)
	if name == "" {
		return cc.InvalidParams()
	}
	path := c.path(name)
	switch {
	case strings.Contains(sw, "ru"):
		return c.loadUsers(path)
	case strings.Contains(sw, "rv"):
		return c.loadVars(path)
	case strings.HasPrefix(sw, "p"):
		return nil // popups other than @window menus are not supported
	}
	kind := script.Remote
	if strings.HasPrefix(sw, "a") {
		kind = script.Aliases
	}
	if _, err := os.Stat(path); err != nil {
		return cc.Errorf("unable to open '%s'", name)
	}
	if old := c.fileByPath(path, kind); old != nil {
		c.rt.Unload(old)
	}
	f, errs := c.rt.LoadFile(path, kind)
	for _, e := range errs {
		c.rt.Out.Error("* /load: " + e.Error())
	}
	if f != nil && events && kind == script.Remote {
		c.rt.Dispatch(interp.Fire{Kind: "on", Name: "LOAD", Only: f})
		c.rt.Dispatch(interp.Fire{Kind: "on", Name: "START", Only: f})
	}
	return nil
}

// cmdUnload implements /unload <-a|-rs> [-n] <file>.
func (c *Control) cmdUnload(cc *interp.CmdCall) error {
	sw, rest := cc.Switches()
	name := strings.Trim(strings.TrimSpace(rest), `"`)
	kind := script.Remote
	if strings.HasPrefix(sw, "a") {
		kind = script.Aliases
	}
	f := c.fileByPath(c.path(name), kind)
	if f == nil {
		return cc.Errorf("'%s' not loaded", name)
	}
	if kind == script.Remote && !strings.Contains(sw, "n") {
		c.rt.Dispatch(interp.Fire{Kind: "on", Name: "UNLOAD", Only: f})
	}
	c.rt.Unload(f)
	return nil
}

// cmdSave implements /save -ru|-rv <file>.
func (c *Control) cmdSave(cc *interp.CmdCall) error {
	sw, rest := cc.Switches()
	path := c.path(strings.Trim(strings.TrimSpace(rest), `"`))
	switch {
	case strings.Contains(sw, "ru"):
		return c.saveUsers(path)
	case strings.Contains(sw, "rv"):
		return c.saveVars(path)
	}
	return nil
}

// Default files for persistent state, in $mircdir.
const (
	VarsFile  = "vars.ini"
	UsersFile = "users.ini"
)

// SaveState writes global variables and the users list to their default files.
func (c *Control) SaveState() error {
	if err := c.saveVars(c.path(VarsFile)); err != nil {
		return err
	}
	return c.saveUsers(c.path(UsersFile))
}

// LoadState reads the default variables and users files if they exist.
func (c *Control) LoadState() {
	if _, err := os.Stat(c.path(VarsFile)); err == nil {
		_ = c.loadVars(c.path(VarsFile))
	}
	if _, err := os.Stat(c.path(UsersFile)); err == nil {
		_ = c.loadUsers(c.path(UsersFile))
	}
}

// saveVars writes global variables in mIRC's vars.ini layout, skipping
// those set to expire.
func (c *Control) saveVars(path string) error {
	var b strings.Builder
	b.WriteString("[variables]\r\n")
	i := 0
	for _, v := range c.rt.Globals.All() {
		if v.UnsetOnExit || !v.UnsetAt.IsZero() {
			continue
		}
		b.WriteString("n" + strconv.Itoa(i) + "=" + v.Name + " " + v.Value + "\r\n")
		i++
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func (c *Control) loadVars(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		_, val, ok := strings.Cut(line, "=")
		if !ok || !strings.HasPrefix(val, "%") {
			continue
		}
		name, value, _ := strings.Cut(val, " ")
		c.rt.Globals.Set(name, value)
	}
	return nil
}

// cmdAlias implements /alias [file] <name> [command]: adds, replaces or
// (without a command) removes a single-line alias.
func (c *Control) cmdAlias(cc *interp.CmdCall) error {
	f := strings.Fields(cc.Args)
	if len(f) == 0 {
		return cc.InvalidParams()
	}
	var target *script.File
	if strings.Contains(f[0], ".") && len(f) > 1 {
		target = c.fileByPath(c.path(f[0]), script.Aliases)
		f = f[1:]
	}
	name := strings.TrimLeft(f[0], "/")
	command := strings.Join(f[1:], " ")
	for _, file := range c.rt.Files() {
		if file.Kind != script.Aliases || (target != nil && file != target) {
			continue
		}
		for i, a := range file.Aliases {
			if !strings.EqualFold(a.Name, name) || len(a.Body) > 1 {
				continue
			}
			if command == "" {
				file.Aliases = append(file.Aliases[:i], file.Aliases[i+1:]...)
			} else {
				parsed, _ := script.Parse(file.Path, name+" "+command, script.Aliases)
				if len(parsed.Aliases) > 0 {
					a.Body = parsed.Aliases[0].Body
				}
			}
			c.rt.Reindex()
			return nil
		}
	}
	if command == "" {
		return nil
	}
	if target == nil {
		for _, file := range c.rt.Files() {
			if file.Kind == script.Aliases {
				target = file
				break
			}
		}
	}
	if target == nil {
		target, _ = c.rt.LoadSource(c.path("aliases.ini"), "", script.Aliases)
	}
	parsed, _ := script.Parse(target.Path, name+" "+command, script.Aliases)
	for _, a := range parsed.Aliases {
		a.File = target
		target.Aliases = append(target.Aliases, a)
	}
	c.rt.Reindex()
	return nil
}

// setGroups implements /enable and /disable <#group ...>, with wildcards.
func (c *Control) setGroups(cc *interp.CmdCall, on bool) error {
	for _, pattern := range cc.Fields() {
		pattern = strings.TrimPrefix(pattern, "#")
		for _, f := range c.rt.Files() {
			for _, g := range f.GroupOrder {
				if interp.WildMatch(pattern, g.Name, false) {
					g.On = on
				}
			}
		}
	}
	return nil
}

func (c *Control) allGroups() []struct {
	g *script.Group
	f *script.File
} {
	var out []struct {
		g *script.Group
		f *script.File
	}
	for _, f := range c.rt.Files() {
		for _, g := range f.GroupOrder {
			out = append(out, struct {
				g *script.Group
				f *script.File
			}{g, f})
		}
	}
	return out
}

func (c *Control) cmdGroups(cc *interp.CmdCall) error {
	sw, _ := cc.Switches()
	for _, x := range c.allGroups() {
		if strings.Contains(sw, "e") && !x.g.On || strings.Contains(sw, "d") && x.g.On {
			continue
		}
		state := "off"
		if x.g.On {
			state = "on"
		}
		c.rt.Out.Echo(interp.StatusWindow, "* #"+x.g.Name+" "+state+" ("+filepath.Base(x.f.Path)+")")
	}
	return nil
}

// identGroup implements $group(N|#name).status|name|fname.
func (c *Control) identGroup(cl *interp.Call) (string, error) {
	all := c.allGroups()
	arg := cl.Arg(0)
	idx := -1
	if n, ok := interp.ParseNumber(arg); ok {
		if int(n) == 0 {
			return strconv.Itoa(len(all)), nil
		}
		idx = int(n) - 1
	} else {
		for i, x := range all {
			if strings.EqualFold("#"+x.g.Name, arg) || strings.EqualFold(x.g.Name, arg) {
				idx = i
				break
			}
		}
	}
	if idx < 0 || idx >= len(all) {
		return "", nil
	}
	x := all[idx]
	status := "off"
	if x.g.On {
		status = "on"
	}
	switch strings.ToLower(cl.Prop) {
	case "status":
		return status, nil
	case "name":
		return "#" + x.g.Name, nil
	case "fname":
		return x.f.Path, nil
	}
	if _, ok := interp.ParseNumber(arg); ok {
		return "#" + x.g.Name, nil
	}
	return status, nil
}

// identAlias implements $alias(N|filename) for alias files.
func (c *Control) identAlias(cl *interp.Call) (string, error) {
	var files []*script.File
	for _, f := range c.rt.Files() {
		if f.Kind == script.Aliases {
			files = append(files, f)
		}
	}
	if n, ok := interp.ParseNumber(cl.Arg(0)); ok {
		i := int(n)
		if i == 0 {
			return strconv.Itoa(len(files)), nil
		}
		if i < 1 || i > len(files) {
			return "", nil
		}
		return files[i-1].Path, nil
	}
	for _, f := range files {
		if strings.EqualFold(filepath.Base(f.Path), filepath.Base(cl.Arg(0))) {
			return f.Path, nil
		}
	}
	return "", nil
}

// identIsalias implements $isalias(name,[N]).fname|alias|ftype.
func (c *Control) identIsalias(cl *interp.Call) (string, error) {
	name := cl.Arg(0)
	var found *script.Alias
	for _, f := range c.rt.Files() {
		for _, a := range f.Aliases {
			if strings.EqualFold(a.Name, name) && (!a.Local || a.File == cl.F.File) {
				found = a
				break
			}
		}
		if found != nil {
			break
		}
	}
	switch strings.ToLower(cl.Prop) {
	case "":
		return interp.Bool(found != nil), nil
	case "fname":
		if found != nil {
			return found.File.Path, nil
		}
	case "ftype":
		if found != nil {
			if found.File.Kind == script.Aliases {
				return "alias", nil
			}
			return "remote", nil
		}
	case "alias":
		if found != nil {
			lines := aliasLines(found)
			n := interp.Int(cl.Arg(1))
			if n == 0 {
				return strings.Join(lines, " | "), nil
			}
			if n <= len(lines) {
				return lines[n-1], nil
			}
		}
	}
	return "", nil
}

// aliasLines renders an alias body's top-level commands.
func aliasLines(a *script.Alias) []string {
	var out []string
	for _, s := range a.Body {
		if cmd, ok := s.(*script.Cmd); ok {
			out = append(out, cmd.Text)
		}
	}
	return out
}

// cmdSignal implements /signal [-n] <name> [parameters]. Without -n the
// signal fires after the current script finishes; the sending script's own
// handlers run first.
func (c *Control) cmdSignal(cc *interp.CmdCall) error {
	sw, rest := cc.Switches()
	name, params, _ := strings.Cut(strings.TrimSpace(rest), " ")
	if name == "" {
		return cc.InvalidParams()
	}
	from := cc.F.File
	cid := cc.F.Cid
	fire := func() {
		ev := &interp.Event{Name: "SIGNAL", Values: map[string]string{"signal": name}}
		c.rt.Dispatch(interp.Fire{
			Kind: "on", Name: "SIGNAL", Event: ev, Params: strings.Fields(params), Cid: cid, First: from,
			Match: func(f *interp.Frame, e *script.Event) (bool, error) {
				return len(e.Fields) > 0 && interp.WildMatch(e.Fields[0], name, false), nil
			},
		})
	}
	if strings.Contains(sw, "n") {
		fire()
		return nil
	}
	c.rt.Post(fire)
	return nil
}
