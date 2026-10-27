package storage

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

func (s *Storage) installDirs() {
	rt := s.rt
	dir := func(sub string) interp.Ident {
		return func(c *interp.Call) (string, error) { return withSlash(filepath.Join(s.Dir, sub)), nil }
	}
	ident(rt, dir(""), "mircdir")
	ident(rt, dir("downloads"), "getdir")
	ident(rt, dir("logs"), "logdir")
	ident(rt, dir("sounds"), "mididir")
	ident(rt, func(c *interp.Call) (string, error) {
		if p := c.F.ScriptName(); p != "" {
			return withSlash(filepath.Dir(p)), nil
		}
		return withSlash(s.Dir), nil
	}, "scriptdir")
	ident(rt, func(c *interp.Call) (string, error) {
		exe, _ := os.Executable()
		return exe, nil
	}, "mircexe")
	ident(rt, func(c *interp.Call) (string, error) { return filepath.Join(s.Dir, "mirc.ini"), nil }, "mircini")
	ident(rt, func(c *interp.Call) (string, error) { return strconv.Itoa(os.Getpid()), nil }, "mircpid")
	ident(rt, func(c *interp.Call) (string, error) { return interp.Bool(true), nil }, "portable")
	ident(rt, s.identSysdir, "sysdir")
	ident(rt, s.identTempfn, "tempfn")
	ident(rt, func(c *interp.Call) (string, error) { return interp.Bool(exists(s.Path(c.Arg(0)))), nil }, "exists")
	ident(rt, func(c *interp.Call) (string, error) {
		st, err := os.Stat(s.Path(c.Arg(0)))
		return interp.Bool(err == nil && !st.IsDir()), nil
	}, "isfile")
	ident(rt, func(c *interp.Call) (string, error) {
		st, err := os.Stat(s.Path(c.Arg(0)))
		return interp.Bool(err == nil && st.IsDir()), nil
	}, "isdir")
	ident(rt, s.identFile, "file")
	ident(rt, func(c *interp.Call) (string, error) {
		return filepath.Base(filepath.FromSlash(strings.ReplaceAll(c.Arg(0), `\`, "/"))), nil
	}, "nopath")
	ident(rt, func(c *interp.Call) (string, error) {
		p := c.Arg(0)
		if i := strings.LastIndexAny(p, `\/`); i >= 0 {
			return p[:i+1], nil
		}
		return "", nil
	}, "nofile")
	ident(rt, func(c *interp.Call) (string, error) { return c.Arg(0), nil }, "longfn", "shortfn")
	ident(rt, func(c *interp.Call) (string, error) { return mkfn(c.Arg(0)), nil }, "mkfn", "mknickfn")
	ident(rt, func(c *interp.Call) (string, error) { return mkfn(c.Arg(0)), nil }, "mklogfn")
	ident(rt, func(c *interp.Call) (string, error) {
		a, err1 := filepath.Abs(s.Path(c.Arg(0)))
		b, err2 := filepath.Abs(s.Path(c.Arg(1)))
		same := err1 == nil && err2 == nil && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
		return interp.Bool(same), nil
	}, "samepath")
	ident(rt, func(c *interp.Call) (string, error) { return s.identFind(c, false) }, "findfile")
	ident(rt, func(c *interp.Call) (string, error) { return s.identFind(c, true) }, "finddir")
	ident(rt, func(c *interp.Call) (string, error) { return strconv.Itoa(s.findn), nil }, "findfilen", "finddirn")
	ident(rt, s.identDisk, "disk")
	ident(rt, identEnvvar, "envvar")
	ident(rt, func(c *interp.Call) (string, error) { return osName(), nil }, "os")
	ident(rt, func(c *interp.Call) (string, error) { return interp.Bool(os.Geteuid() == 0), nil }, "isadmin")

	rt.RegisterCommand("mkdir", interp.Command{Fn: func(c *interp.CmdCall) error {
		name, _ := cutFilename(c.Args)
		return fileErr(c, os.MkdirAll(s.Path(name), 0o755))
	}})
	rt.RegisterCommand("rmdir", interp.Command{Fn: func(c *interp.CmdCall) error {
		name, _ := cutFilename(c.Args)
		return fileErr(c, os.Remove(s.Path(name)))
	}})
	rt.RegisterCommand("remove", interp.Command{Fn: func(c *interp.CmdCall) error {
		_, rest := c.Switches()
		name, _ := cutFilename(rest)
		err := os.Remove(s.Path(name))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fileErr(c, err)
	}})
	rt.RegisterCommand("rename", interp.Command{Fn: s.cmdRename})
	rt.RegisterCommand("copy", interp.Command{Fn: s.cmdCopy})
	rt.RegisterCommand("run", interp.Command{Fn: s.cmdRun})
}

func withSlash(p string) string {
	if !strings.HasSuffix(p, string(filepath.Separator)) {
		return p + string(filepath.Separator)
	}
	return p
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func fileErr(c *interp.CmdCall, err error) error {
	if err != nil {
		return c.Errorf("%s", err.Error())
	}
	return nil
}

// mkfn replaces characters that are not allowed in filenames.
func mkfn(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, r) || r < 32 {
			return '_'
		}
		return r
	}, s)
}

func osName() string {
	switch runtime.GOOS {
	case "windows":
		return "11"
	case "darwin":
		return "macOS"
	}
	return runtime.GOOS
}

func (s *Storage) identSysdir(c *interp.Call) (string, error) {
	home, _ := os.UserHomeDir()
	switch strings.ToLower(c.Arg(0)) {
	case "profile":
		return withSlash(home), nil
	case "desktop":
		return withSlash(filepath.Join(home, "Desktop")), nil
	case "documents":
		return withSlash(filepath.Join(home, "Documents")), nil
	case "downloads":
		return withSlash(filepath.Join(home, "Downloads")), nil
	case "music":
		return withSlash(filepath.Join(home, "Music")), nil
	case "pictures":
		return withSlash(filepath.Join(home, "Pictures")), nil
	case "videos":
		return withSlash(filepath.Join(home, "Videos")), nil
	}
	return "", nil
}

func (s *Storage) identTempfn(c *interp.Call) (string, error) {
	dir := s.Dir
	if c.Arg(0) != "" {
		dir = s.Path(c.Arg(0))
	}
	for i := 0; ; i++ {
		p := filepath.Join(dir, "mirc"+strconv.FormatInt(time.Now().UnixNano()%1e8+int64(i), 36)+".tmp")
		if !exists(p) {
			return p, nil
		}
	}
}

// identFile implements $file(name).size|ctime|mtime|atime|shortfn|longfn|attr|path|name|ext.
func (s *Storage) identFile(c *interp.Call) (string, error) {
	p := s.Path(c.Arg(0))
	st, err := os.Stat(p)
	if err != nil {
		return "", nil
	}
	switch strings.ToLower(c.Prop) {
	case "size", "":
		if c.Prop == "" {
			return p, nil
		}
		return strconv.FormatInt(st.Size(), 10), nil
	case "ctime", "mtime", "atime":
		return strconv.FormatInt(st.ModTime().Unix(), 10), nil
	case "shortfn", "longfn":
		return p, nil
	case "attr":
		if st.IsDir() {
			return "d", nil
		}
		if st.Mode().Perm()&0o200 == 0 {
			return "r", nil
		}
		return "a", nil
	case "path":
		return withSlash(filepath.Dir(p)), nil
	case "name":
		return strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)), nil
	case "ext":
		return filepath.Ext(p), nil
	}
	return "", nil
}

// identFind implements $findfile and $finddir(dir,wildcard,N,depth,command).
func (s *Storage) identFind(c *interp.Call, dirs bool) (string, error) {
	root := s.Path(c.Arg(0))
	patterns := strings.Split(c.Arg(1), ";")
	n := interp.Int(c.Arg(2))
	depth := -1
	command := ""
	if len(c.Args) > 3 {
		if d, ok := interp.ParseNumber(c.Arg(3)); ok {
			depth = int(d)
			command = strings.Join(c.Args[4:], ",")
		} else {
			command = strings.Join(c.Args[3:], ",")
		}
	}
	count := 0
	result := ""
	var cmdErr error
	stop := errors.New("stop")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() && depth >= 0 && strings.Count(rel, string(filepath.Separator)) >= depth {
			return filepath.SkipDir
		}
		if d.IsDir() != dirs {
			return nil
		}
		matched := false
		for _, p := range patterns {
			if interp.WildMatch(strings.TrimSpace(p), d.Name(), false) {
				matched = true
				break
			}
		}
		if !matched {
			return nil
		}
		count++
		if command != "" {
			f := c.F.RT.NewFrame(c.F.File, c.F.Event)
			f.Params = strings.Fields(path)
			if err := f.ExecLine(command, 0); err != nil {
				if interp.IsHalt(err) {
					return stop
				}
				cmdErr = err
				return stop
			}
		}
		if n > 0 && count == n {
			result = path
			return stop
		}
		return nil
	})
	if cmdErr != nil {
		return "", cmdErr
	}
	if err != nil && err != stop {
		return "", nil
	}
	s.findn = count
	if n == 0 {
		return strconv.Itoa(count), nil
	}
	return result, nil
}

func (s *Storage) identDisk(c *interp.Call) (string, error) {
	p := s.Path(c.Arg(0))
	if n, ok := interp.ParseNumber(c.Arg(0)); ok && int(n) == 0 {
		return "1", nil
	}
	st, err := os.Stat(p)
	ok := err == nil && st.IsDir()
	switch strings.ToLower(c.Prop) {
	case "":
		return interp.Bool(ok), nil
	case "path":
		return withSlash(p), nil
	case "type":
		return "fixed", nil
	}
	return "", nil
}

func identEnvvar(c *interp.Call) (string, error) {
	arg := c.Arg(0)
	if n, ok := interp.ParseNumber(arg); ok {
		env := os.Environ()
		i := int(n)
		if i == 0 {
			return strconv.Itoa(len(env)), nil
		}
		if i < 1 || i > len(env) {
			return "", nil
		}
		k, v, _ := strings.Cut(env[i-1], "=")
		if strings.EqualFold(c.Prop, "value") {
			return v, nil
		}
		return k, nil
	}
	return os.Getenv(arg), nil
}

// cmdRename implements /rename [-fo] <file> <newfile>.
func (s *Storage) cmdRename(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	from, rest := cutFilename(rest)
	to, _ := cutFilename(rest)
	dst := s.Path(to)
	if exists(dst) && !strings.Contains(sw, "o") {
		return c.Errorf("unable to rename '%s'", from)
	}
	return fileErr(c, os.Rename(s.Path(from), dst))
}

// cmdCopy implements /copy [-aofp] <file> <file|dir>, with wildcard sources.
func (s *Storage) cmdCopy(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	from, rest := cutFilename(rest)
	to, _ := cutFilename(rest)
	srcs, _ := filepath.Glob(s.Path(from))
	if len(srcs) == 0 {
		return c.Errorf("unable to open '%s'", from)
	}
	dst := s.Path(to)
	for _, src := range srcs {
		target := dst
		if st, err := os.Stat(dst); err == nil && st.IsDir() {
			target = filepath.Join(dst, filepath.Base(src))
		}
		if exists(target) && !strings.ContainsAny(sw, "ao") {
			return c.Errorf("unable to copy to '%s'", to)
		}
		if err := copyFile(src, target, strings.Contains(sw, "a")); err != nil {
			return fileErr(c, err)
		}
	}
	return nil
}

func copyFile(src, dst string, appendTo bool) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if appendTo {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	out, err := os.OpenFile(dst, flags, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// cmdRun implements /run [-ahnp] <file> [parameters]: the program starts
// in the background and is not waited for.
func (s *Storage) cmdRun(c *interp.CmdCall) error {
	sw, rest := c.Switches()
	name, params := cutFilename(rest)
	if name == "" {
		return c.InvalidParams()
	}
	path := name
	if p := s.Path(name); exists(p) {
		path = p
	}
	cmd := exec.Command(path, splitQuoted(params)...)
	if strings.Contains(sw, "p") {
		cmd.Dir = filepath.Dir(path)
	} else {
		cmd.Dir = s.Dir
	}
	if err := cmd.Start(); err != nil {
		if err := openCommand(path).Start(); err == nil {
			return nil
		}
		return c.Errorf("unable to run '%s'", name)
	}
	go cmd.Wait()
	return nil
}

// openCommand opens a non-executable file with its associated application.
func openCommand(path string) *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path)
	case "darwin":
		return exec.Command("open", path)
	}
	return exec.Command("xdg-open", path)
}

// splitQuoted splits parameters on spaces, keeping "quoted strings" whole.
func splitQuoted(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ' ' && !quoted:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
