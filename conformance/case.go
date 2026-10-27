// Package conformance holds behaviour cases the runtime must reproduce.
//
// A case is a txtar-style file under testdata/:
//
//	Free-form description; the first line is the summary.
//	-- origin --
//	spec            expectation derived from the help file
//	mirc-7.85       expectation recorded from a real mIRC 7.85 run
//	-- script.mrc --
//	alias test { echo -s $gettok(a.b.c,2,46) }
//	-- output --
//	b
//
// The runner loads script.mrc, calls the "test" alias, and compares every
// line the script echoes (in order) with output. Cases with origin "spec" are
// provisional: a real mIRC run may overrule them.
package conformance

import (
	"bytes"
	"fmt"
	"strings"
)

// Origins of an expectation.
const (
	OriginSpec = "spec"
	OriginMIRC = "mirc-7.85"
)

// Case is one parsed conformance file.
type Case struct {
	Name    string
	Comment string
	Origin  string
	Files   map[string]string
	Output  []string
}

// Parse reads a case file. name is used in errors and reports.
func Parse(name string, data []byte) (Case, error) {
	c := Case{Name: name, Files: map[string]string{}}
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))

	var section string
	var body strings.Builder
	flush := func() {
		if section == "" {
			c.Comment = strings.TrimSpace(body.String())
		} else {
			c.Files[section] = body.String()
		}
		body.Reset()
	}
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if header, ok := sectionHeader(line); ok {
			flush()
			if _, dup := c.Files[header]; dup {
				return c, fmt.Errorf("%s: duplicate section %q", name, header)
			}
			section = header
			continue
		}
		body.WriteString(line)
	}
	flush()

	c.Origin = strings.TrimSpace(c.Files["origin"])
	if c.Origin != OriginSpec && c.Origin != OriginMIRC {
		return c, fmt.Errorf("%s: origin must be %q or %q, got %q", name, OriginSpec, OriginMIRC, c.Origin)
	}
	if _, ok := c.Files["script.mrc"]; !ok {
		return c, fmt.Errorf("%s: missing script.mrc section", name)
	}
	out, ok := c.Files["output"]
	if !ok {
		return c, fmt.Errorf("%s: missing output section", name)
	}
	if out = strings.TrimSuffix(out, "\n"); out != "" {
		c.Output = strings.Split(out, "\n")
	}
	delete(c.Files, "origin")
	delete(c.Files, "output")
	return c, nil
}

func sectionHeader(line string) (string, bool) {
	line = strings.TrimSuffix(line, "\n")
	if !strings.HasPrefix(line, "-- ") || !strings.HasSuffix(line, " --") || len(line) < 7 {
		return "", false
	}
	return strings.TrimSpace(line[3 : len(line)-3]), true
}
