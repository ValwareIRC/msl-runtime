package conformance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCasesParse(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "*", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no conformance cases found")
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(p, data); err != nil {
			t.Error(err)
		}
	}
}

func TestParseRejectsBadOrigin(t *testing.T) {
	_, err := Parse("bad", []byte("x\n-- origin --\nguess\n-- script.mrc --\n-- output --\n"))
	if err == nil {
		t.Fatal("want error for unknown origin")
	}
}
