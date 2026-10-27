package conformance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ValwareIRC/msl-runtime/internal/builtins"
	"github.com/ValwareIRC/msl-runtime/internal/data"
	"github.com/ValwareIRC/msl-runtime/internal/interp"
	"github.com/ValwareIRC/msl-runtime/internal/storage"
)

func TestCases(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "*", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		t.Run(filepath.ToSlash(p[len("testdata")+1:]), func(t *testing.T) {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			c, err := Parse(p, raw)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			got, err := Run(c, func(rt *interp.Runtime) {
				builtins.Install(rt)
				storage.Install(rt, dir)
				data.Install(rt)
			})
			if err != nil {
				t.Fatal(err)
			}
			if d := Diff(c.Output, got); d != "" {
				t.Errorf("%s (origin %s)\n%s", c.Comment, c.Origin, d)
			}
		})
	}
}
