// Package storage provides mIRC's file, directory and INI commands and
// identifiers. Relative paths are resolved against the runtime's data
// directory ($mircdir).
package storage

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/interp"
)

// Storage is the per-runtime file state.
type Storage struct {
	rt *interp.Runtime
	// Dir is $mircdir, the base of relative paths.
	Dir string

	inis   map[string]*iniFile
	files  []*openFile
	feof   bool
	ferr   bool
	readn  int
	filter int
	findn  int

	// Binvars and SetBinvar give access to &binvars; the data package sets them.
	Binvars   func(f *interp.Frame, name string) []byte
	SetBinvar func(f *interp.Frame, name string, data []byte)
	// Windows serves /filter with window buffers; the window package sets it.
	Windows WindowLines
}

// WindowLines reads and writes the text of runtime windows.
type WindowLines interface {
	Lines(f *interp.Frame, name string) ([]string, bool)
	SetLines(f *interp.Frame, name string, lines []string, clear bool) bool
}

type storageKey struct{}

// Install registers the storage commands and identifiers.
func Install(rt *interp.Runtime, dir string) *Storage {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	s := &Storage{rt: rt, Dir: dir, inis: map[string]*iniFile{}}
	interp.Ext(rt, storageKey{}, func() *Storage { return s })
	s.installFiles()
	s.installINI()
	s.installDirs()
	s.installHandles()
	return s
}

// From returns the storage installed on rt.
func From(rt *interp.Runtime) *Storage {
	return interp.Ext(rt, storageKey{}, func() *Storage { return nil })
}

// Path resolves a script-supplied filename: quotes are removed and
// relative paths are taken from $mircdir.
func (s *Storage) Path(name string) string {
	name = strings.TrimSpace(name)
	if len(name) >= 2 && name[0] == '"' && name[len(name)-1] == '"' {
		name = name[1 : len(name)-1]
	}
	name = filepath.FromSlash(strings.ReplaceAll(name, `\`, "/"))
	if name == "" || filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(s.Dir, name)
}

// cutFilename splits a possibly quoted filename from the front of text.
func cutFilename(text string) (name, rest string) {
	text = strings.TrimLeft(text, " ")
	if strings.HasPrefix(text, `"`) {
		if end := strings.IndexByte(text[1:], '"'); end >= 0 {
			return text[1 : end+1], strings.TrimLeft(text[end+2:], " ")
		}
	}
	name, rest, _ = strings.Cut(text, " ")
	return name, rest
}

func ident(rt *interp.Runtime, fn interp.Ident, names ...string) {
	for _, n := range names {
		rt.RegisterIdent(n, fn)
	}
}
