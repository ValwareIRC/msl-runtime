// Command specgen builds the mIRC scripting inventory from an extracted mirc.chm.
//
// It records facts only (names, syntax lines, page anchors, scope), never the
// help prose, and writes internal/spec/inventory.json.
//
//	hh.exe -decompile <dir> mirc.chm
//	go run ./tools/specgen -chm <dir>
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ValwareIRC/msl-runtime/internal/spec"
)

var (
	reEntry     = regexp.MustCompile(`(?s)<OBJECT type="text/sitemap">(.*?)</OBJECT>`)
	reParam     = regexp.MustCompile(`<param name="(Name|Local)" value="([^"]*)">`)
	reTag       = regexp.MustCompile(`<[^>]+>`)
	reSpaces    = regexp.MustCompile(`\s+`)
	reParagraph = regexp.MustCompile(`(?s)<p[^>]*>(.*?)</p>`)
)

// maxSyntaxParagraphs bounds how far past an anchor a syntax line is searched for.
const maxSyntaxParagraphs = 12

type scopeFile struct {
	Pages map[string]spec.Scope `json:"pages"`
	Names map[string]spec.Scope `json:"names"`
}

func main() {
	chm := flag.String("chm", "", "directory holding the decompiled mirc.chm")
	scopePath := flag.String("scope", "spec/scope.json", "scope rules")
	out := flag.String("out", "internal/spec/inventory.json", "output file")
	flag.Parse()
	if *chm == "" {
		log.Fatal("specgen: -chm is required")
	}

	scope, err := readScope(*scopePath)
	if err != nil {
		log.Fatal(err)
	}
	hhk, err := filepath.Glob(filepath.Join(*chm, "*.hhk"))
	if err != nil || len(hhk) != 1 {
		log.Fatalf("specgen: expected one .hhk index in %s", *chm)
	}
	index, err := os.ReadFile(hhk[0])
	if err != nil {
		log.Fatal(err)
	}

	pages := map[string]string{}
	byName := map[string]*spec.Entry{}
	for _, m := range reEntry.FindAllStringSubmatch(string(index), -1) {
		name, local := parseEntry(m[1])
		if name == "" || local == "" {
			continue
		}
		page, anchor, _ := strings.Cut(local, "#")
		page = strings.TrimSuffix(page, ".html")
		body, ok := pages[page]
		if !ok {
			b, err := os.ReadFile(filepath.Join(*chm, page+".html"))
			if err != nil {
				log.Fatalf("specgen: index points at missing page %s: %v", page, err)
			}
			body = string(b)
			pages[page] = body
		}

		e := byName[name]
		if e == nil {
			e = &spec.Entry{Name: name, Kind: spec.KindOf(name)}
			byName[name] = e
		}
		e.Refs = append(e.Refs, spec.Ref{Page: page, Anchor: anchor, Syntax: syntaxAt(body, name, anchor)})
	}

	entries := make([]spec.Entry, 0, len(byName))
	for _, e := range byName {
		e.Scope = decide(scope, *e)
		entries = append(entries, *e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	data, err := json.MarshalIndent(spec.Inventory{Source: "mIRC v7.85 mirc.chm", Entries: entries}, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	report(entries)
}

func readScope(path string) (scopeFile, error) {
	var s scopeFile
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("specgen: %s: %w", path, err)
	}
	return s, nil
}

// parseEntry reads one index item. The first Name param is the keyword; the
// second is the topic title, which is ignored.
func parseEntry(obj string) (name, local string) {
	for _, p := range reParam.FindAllStringSubmatch(obj, -1) {
		v := html.UnescapeString(p[2])
		switch {
		case p[1] == "Local":
			local = v
		case name == "":
			name = v
		}
	}
	return strings.TrimSpace(name), local
}

// syntaxAt returns the syntax line documenting name: the first paragraph at
// or after the anchor (or the page heading) that begins with the name itself,
// or a "Format:" line for events.
func syntaxAt(body, name, anchor string) string {
	start := strings.Index(body, `class="f_Heading1"`)
	if anchor != "" {
		start = strings.Index(body, `<a name="`+anchor+`"`)
	}
	if start < 0 {
		return ""
	}
	// Begin at the paragraph that holds the anchor; by convention it is the
	// bold syntax line, which may document a sibling ($asin under $sin).
	start = max(0, strings.LastIndex(body[:start], "<p"))
	key := strings.ToLower(strings.Fields(name)[0])
	if strings.HasPrefix(key, "on") || strings.HasPrefix(key, "raw") || strings.HasPrefix(key, "ctcp") {
		key = ""
	}
	paras := reParagraph.FindAllStringSubmatch(body[start:], maxSyntaxParagraphs)
	if anchor != "" && len(paras) > 0 {
		if text := plainText(paras[0][1]); key != "" && strings.HasPrefix(text, key[:1]) {
			return text
		}
	}
	for _, p := range paras {
		text := plainText(p[1])
		if format, ok := strings.CutPrefix(text, "Format:"); ok {
			return strings.TrimSpace(format)
		}
		if key != "" && strings.HasPrefix(strings.ToLower(text), key) {
			return text
		}
	}
	return ""
}

// plainText strips markup and collapses whitespace, including &nbsp;.
func plainText(s string) string {
	text := html.UnescapeString(reTag.ReplaceAllString(s, ""))
	return strings.TrimSpace(reSpaces.ReplaceAllString(strings.ReplaceAll(text, " ", " "), " "))
}

// decide applies a name override, else the strictest rule among the entry's pages:
// an entry documented on any in-scope page is in scope.
func decide(s scopeFile, e spec.Entry) spec.Scope {
	if sc, ok := s.Names[e.Name]; ok {
		return sc
	}
	var best spec.Scope
	for i, r := range e.Refs {
		sc, ok := s.Pages[r.Page]
		if !ok {
			return spec.Scope{Scope: spec.ScopeIn}
		}
		if i == 0 || rank(sc.Scope) > rank(best.Scope) {
			best = sc
		}
	}
	return best
}

func rank(s string) int {
	switch s {
	case spec.ScopeIn:
		return 2
	case spec.ScopeGuide:
		return 1
	}
	return 0
}

func report(entries []spec.Entry) {
	counts := map[string]map[string]int{}
	for _, e := range entries {
		if counts[e.Kind] == nil {
			counts[e.Kind] = map[string]int{}
		}
		counts[e.Kind][e.Scope.Scope]++
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	fmt.Printf("%d entries\n", len(entries))
	for _, k := range kinds {
		c := counts[k]
		fmt.Printf("  %-10s in=%-4d guide=%-4d out=%d\n", k, c[spec.ScopeIn], c[spec.ScopeGuide], c[spec.ScopeOut])
	}
}
