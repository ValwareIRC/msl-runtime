package storage

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

// iniFile is an INI file held in memory, preserving section and item order.
type iniFile struct {
	path     string
	sections []*iniSection
}

type iniSection struct {
	name  string
	items []iniItem
}

type iniItem struct{ key, value string }

func (f *iniFile) section(name string) *iniSection {
	for _, s := range f.sections {
		if strings.EqualFold(s.name, name) {
			return s
		}
	}
	return nil
}

func (s *iniSection) find(key string) int {
	for i, it := range s.items {
		if strings.EqualFold(it.key, key) {
			return i
		}
	}
	return -1
}

// ini returns a cached INI file, loading it from disk on first use.
func (s *Storage) ini(name string) *iniFile {
	path := s.Path(name)
	if f, ok := s.inis[strings.ToLower(path)]; ok {
		return f
	}
	f := &iniFile{path: path}
	if fh, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		var cur *iniSection
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), "\r")
			if t := strings.TrimSpace(line); strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
				cur = &iniSection{name: t[1 : len(t)-1]}
				f.sections = append(f.sections, cur)
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok || cur == nil {
				continue
			}
			cur.items = append(cur.items, iniItem{k, v})
		}
		fh.Close()
	}
	s.inis[strings.ToLower(path)] = f
	return f
}

// save writes an INI file back to disk.
func (f *iniFile) save() error {
	var b strings.Builder
	for _, s := range f.sections {
		b.WriteString("[" + s.name + "]\r\n")
		for _, it := range s.items {
			b.WriteString(it.key + "=" + it.value + "\r\n")
		}
	}
	return os.WriteFile(f.path, []byte(b.String()), 0o644)
}

func (s *Storage) installINI() {
	s.rt.RegisterCommand("writeini", interp.Command{Fn: s.cmdWriteini})
	s.rt.RegisterCommand("remini", interp.Command{Fn: s.cmdRemini})
	s.rt.RegisterCommand("flushini", interp.Command{Fn: func(c *interp.CmdCall) error {
		name, _ := cutFilename(c.Args)
		return s.ini(name).save()
	}})
	s.rt.RegisterCommand("copyini", interp.Command{Fn: s.cmdCopyini})
	ident(s.rt, s.identReadini, "readini")
	ident(s.rt, s.identIni, "ini")
}

// cmdWriteini implements /writeini [-nz] <file> <section> <item> <value>.
func (s *Storage) cmdWriteini(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	name, rest := cutFilename(rest)
	f := strings.SplitN(rest, " ", 3)
	if len(f) < 2 || (len(f) < 3 && !strings.Contains(sw, "z")) {
		return c.InvalidParams()
	}
	value := ""
	if len(f) == 3 {
		value = f[2]
	}
	ini := s.ini(name)
	sec := ini.section(f[0])
	if sec == nil {
		sec = &iniSection{name: f[0]}
		ini.sections = append(ini.sections, sec)
	}
	if i := sec.find(f[1]); i >= 0 {
		sec.items[i].value = value
	} else {
		sec.items = append(sec.items, iniItem{f[1], value})
	}
	return ini.save()
}

// cmdRemini implements /remini <file> <section> [item].
func (s *Storage) cmdRemini(c *interp.CmdCall) error {
	name, rest := cutFilename(c.Args)
	section, item, _ := strings.Cut(strings.TrimSpace(rest), " ")
	ini := s.ini(name)
	for i, sec := range ini.sections {
		if !strings.EqualFold(sec.name, section) {
			continue
		}
		if item == "" {
			ini.sections = append(ini.sections[:i], ini.sections[i+1:]...)
		} else if j := sec.find(item); j >= 0 {
			sec.items = append(sec.items[:j], sec.items[j+1:]...)
		}
		break
	}
	return ini.save()
}

// cmdCopyini implements /copyini <file> <section> <newsection>.
func (s *Storage) cmdCopyini(c *interp.CmdCall) error {
	name, rest := cutFilename(c.Args)
	f := strings.Fields(rest)
	if len(f) < 2 {
		return c.InvalidParams()
	}
	ini := s.ini(name)
	src := ini.section(f[0])
	if src == nil {
		return nil
	}
	dst := &iniSection{name: f[1], items: append([]iniItem(nil), src.items...)}
	if old := ini.section(f[1]); old != nil {
		old.items = dst.items
	} else {
		ini.sections = append(ini.sections, dst)
	}
	return ini.save()
}

// identReadini implements $readini(file, [np], section, item). The value is
// evaluated unless the n switch is given.
func (s *Storage) identReadini(c *interp.Call) (string, error) {
	args := c.Args
	opts := ""
	if len(args) == 4 {
		opts, args = args[1], append([]string{args[0]}, args[2:]...)
	}
	if len(args) < 3 {
		return "", c.InvalidParams()
	}
	sec := s.ini(args[0]).section(args[1])
	if sec == nil {
		return "", nil
	}
	i := sec.find(args[2])
	if i < 0 {
		return "", nil
	}
	v := sec.items[i].value
	if strings.Contains(opts, "n") {
		return v, nil
	}
	return c.F.Eval(v)
}

// identIni implements $ini(file, topic|N, [item|N]).
func (s *Storage) identIni(c *interp.Call) (string, error) {
	ini := s.ini(c.Arg(0))
	if len(c.Args) < 2 {
		return "", c.InvalidParams()
	}
	names := make([]string, len(ini.sections))
	for i, sec := range ini.sections {
		names[i] = sec.name
	}
	if len(c.Args) < 3 {
		return nthOrIndex(c.Arg(1), names), nil
	}
	var sec *iniSection
	if n, ok := interp.ParseNumber(c.Arg(1)); ok {
		if int(n) >= 1 && int(n) <= len(ini.sections) {
			sec = ini.sections[int(n)-1]
		}
	} else {
		sec = ini.section(c.Arg(1))
	}
	if sec == nil {
		return "", nil
	}
	keys := make([]string, len(sec.items))
	for i, it := range sec.items {
		keys[i] = it.key
	}
	return nthOrIndex(c.Arg(2), keys), nil
}

// nthOrIndex returns the count for 0, the Nth name for N, or the position
// of a name (0 when absent).
func nthOrIndex(arg string, names []string) string {
	if n, ok := interp.ParseNumber(arg); ok {
		i := int(n)
		if i == 0 {
			return strconv.Itoa(len(names))
		}
		if i < 1 || i > len(names) {
			return ""
		}
		return names[i-1]
	}
	for i, n := range names {
		if strings.EqualFold(n, arg) {
			return strconv.Itoa(i + 1)
		}
	}
	return "0"
}
