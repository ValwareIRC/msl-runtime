package storage

import (
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/regex"
)

// openFile is a /fopen handle.
type openFile struct {
	name string
	path string
	f    *os.File
	eof  bool
	err  bool
}

func (s *Storage) installHandles() {
	rt := s.rt
	rt.RegisterCommand("fopen", interp.Command{Fn: s.cmdFopen})
	rt.RegisterCommand("fclose", interp.Command{Fn: s.cmdFclose})
	rt.RegisterCommand("flist", interp.Command{Fn: s.cmdFlist})
	rt.RegisterCommand("fseek", interp.Command{Fn: s.cmdFseek})
	rt.RegisterCommand("fwrite", interp.Command{Fn: s.cmdFwrite})
	ident(rt, s.identFopen, "fopen")
	ident(rt, s.identFread, "fread")
	ident(rt, s.identFgetc, "fgetc")
	ident(rt, func(c *interp.Call) (string, error) { return interp.Bool(s.feof), nil }, "feof")
	ident(rt, func(c *interp.Call) (string, error) { return interp.Bool(s.ferr), nil }, "ferr")
}

func (s *Storage) handle(arg string) *openFile {
	if n, ok := interp.ParseNumber(arg); ok {
		if i := int(n); i >= 1 && i <= len(s.files) {
			return s.files[i-1]
		}
		return nil
	}
	for _, f := range s.files {
		if strings.EqualFold(f.name, arg) {
			return f
		}
	}
	return nil
}

func (s *Storage) result(f *openFile, eof, err bool) {
	s.feof, s.ferr = eof, err
	if f != nil {
		f.eof, f.err = eof, err
	}
}

// cmdFopen implements /fopen [-nox] <name> <filename>. Failures do not halt
// the script; they set $ferr.
func (s *Storage) cmdFopen(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	name, rest := cutFilename(rest)
	file, _ := cutFilename(rest)
	if name == "" || file == "" {
		return c.InvalidParams()
	}
	if s.handle(name) != nil {
		return c.Errorf("'%s' name in use", name)
	}
	flags := os.O_RDWR
	switch {
	case strings.Contains(sw, "o"):
		flags |= os.O_CREATE | os.O_TRUNC
	case strings.Contains(sw, "n"):
		flags |= os.O_CREATE | os.O_EXCL
	}
	path := s.Path(file)
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		if f, err = os.Open(path); err != nil {
			s.result(nil, false, true)
			return nil
		}
	}
	s.files = append(s.files, &openFile{name: name, path: path, f: f})
	s.result(nil, false, false)
	return nil
}

func (s *Storage) cmdFclose(c *interp.CmdCall) error {
	pattern := strings.TrimSpace(c.Args)
	kept := s.files[:0]
	for _, f := range s.files {
		if interp.WildMatch(pattern, f.name, false) {
			f.f.Close()
			continue
		}
		kept = append(kept, f)
	}
	s.files = kept
	return nil
}

func (s *Storage) cmdFlist(c *interp.CmdCall) error {
	pattern := strings.TrimSpace(c.Args)
	if pattern == "" {
		pattern = "*"
	}
	for _, f := range s.files {
		if interp.WildMatch(pattern, f.name, false) {
			c.F.RT.Out.Echo(interp.StatusWindow, "* "+f.name+" "+f.path)
		}
	}
	return nil
}

// cmdFseek implements /fseek [-lnpwr] <name> <position|line|wildcard|regex>.
func (s *Storage) cmdFseek(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	name, arg, _ := strings.Cut(strings.TrimSpace(rest), " ")
	h := s.handle(name)
	if h == nil {
		return c.Errorf("'%s' not open", name)
	}
	switch {
	case strings.ContainsAny(sw, "lwr"):
		data, err := os.ReadFile(h.path)
		if err != nil {
			s.result(h, false, true)
			return nil
		}
		lines := strings.SplitAfter(string(data), "\n")
		pos := int64(0)
		target := -1
		for i, l := range lines {
			text := strings.TrimRight(l, "\r\n")
			var hit bool
			switch {
			case strings.Contains(sw, "l"):
				hit = i+1 == interp.Int(arg)
			case strings.Contains(sw, "w"):
				hit = interp.WildMatch(arg, text, false)
			default:
				re, err := regex.Compile(arg)
				if err == nil {
					hit, _ = re.MatchString(text)
				}
			}
			if hit {
				target = int(pos)
				break
			}
			pos += int64(len(l))
		}
		if target < 0 {
			h.f.Seek(0, io.SeekEnd)
			s.result(h, true, false)
			return nil
		}
		h.f.Seek(int64(target), io.SeekStart)
	case strings.Contains(sw, "n"):
		readLine(h.f)
	case strings.Contains(sw, "p"):
		cur, _ := h.f.Seek(0, io.SeekCurrent)
		h.f.Seek(lineStart(h.path, cur), io.SeekStart)
	default:
		h.f.Seek(int64(interp.Int(arg)), io.SeekStart)
	}
	s.result(h, false, false)
	return nil
}

// lineStart returns the start of the line containing pos, or of the
// previous line when pos is already at a line start.
func lineStart(path string, pos int64) int64 {
	data, err := os.ReadFile(path)
	if err != nil || pos <= 0 {
		return 0
	}
	if pos > int64(len(data)) {
		pos = int64(len(data))
	}
	i := pos - 1
	if data[i] == '\n' {
		i--
	}
	for i >= 0 && data[i] != '\n' {
		i--
	}
	return i + 1
}

// readLine reads up to and including the next \n.
func readLine(f *os.File) (string, bool) {
	var b []byte
	buf := make([]byte, 1)
	for {
		n, err := f.Read(buf)
		if n == 0 || err != nil {
			return string(b), len(b) == 0
		}
		if buf[0] == '\n' {
			return string(b), false
		}
		b = append(b, buf[0])
	}
}

// cmdFwrite implements /fwrite [-bn] <name> <text | &binvar>.
func (s *Storage) cmdFwrite(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	name, text, _ := strings.Cut(rest, " ")
	h := s.handle(name)
	if h == nil {
		return c.Errorf("'%s' not open", name)
	}
	data := []byte(text)
	if strings.Contains(sw, "b") && s.Binvars != nil {
		data = s.Binvars(c.F, strings.TrimSpace(text))
	}
	if strings.Contains(sw, "n") {
		data = append(data, '\r', '\n')
	}
	_, err := h.f.Write(data)
	s.result(h, false, err != nil)
	return nil
}

func (s *Storage) identFopen(c *interp.Call) (string, error) {
	if n, ok := interp.ParseNumber(c.Arg(0)); ok && int(n) == 0 {
		return strconv.Itoa(len(s.files)), nil
	}
	h := s.handle(c.Arg(0))
	if h == nil {
		return "", nil
	}
	switch strings.ToLower(c.Prop) {
	case "fname":
		return h.path, nil
	case "pos":
		pos, _ := h.f.Seek(0, io.SeekCurrent)
		return strconv.FormatInt(pos, 10), nil
	case "eof":
		return interp.Bool(h.eof), nil
	case "err":
		return interp.Bool(h.err), nil
	}
	return h.name, nil
}

// identFread implements $fread(name) (next line) and
// $fread(name, M, &binvar) (M bytes into a binvar).
func (s *Storage) identFread(c *interp.Call) (string, error) {
	h := s.handle(c.Arg(0))
	if h == nil {
		s.result(nil, false, true)
		return "", nil
	}
	if len(c.Args) >= 3 && s.SetBinvar != nil {
		buf := make([]byte, max(0, interp.Int(c.Arg(1))))
		n, err := io.ReadFull(h.f, buf)
		s.result(h, n == 0 && err != nil, false)
		s.SetBinvar(c.F, c.Arg(2), buf[:n])
		return strconv.Itoa(n), nil
	}
	line, eof := readLine(h.f)
	s.result(h, eof, false)
	return strings.TrimRight(line, "\r"), nil
}

func (s *Storage) identFgetc(c *interp.Call) (string, error) {
	h := s.handle(c.Arg(0))
	if h == nil {
		s.result(nil, false, true)
		return "", nil
	}
	var b [1]byte
	if n, _ := h.f.Read(b[:]); n == 0 {
		s.result(h, true, false)
		return "", nil
	}
	s.result(h, false, false)
	return strconv.Itoa(int(b[0])), nil
}
