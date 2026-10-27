// Command coverage reports how much of the mIRC inventory the runtime implements.
//
//	go run ./tools/coverage          # summary
//	go run ./tools/coverage -json    # full report for tooling
//	go run ./tools/coverage -missing # in-scope entries still to do
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/ValwareIRC/msl-runtime/internal/coverage"
	"github.com/ValwareIRC/msl-runtime/internal/engine"
	"github.com/ValwareIRC/msl-runtime/internal/spec"
)

type discard struct{}

func (discard) Echo(string, string) {}
func (discard) Error(string)        {}

func main() {
	asJSON := flag.Bool("json", false, "print the full report as JSON")
	missing := flag.Bool("missing", false, "list in-scope entries not yet implemented")
	flag.Parse()

	inv, err := spec.Load()
	if err != nil {
		log.Fatal(err)
	}
	eng := engine.New(discard{}, "")
	rep := coverage.Compute(eng.RT, inv, eng.RT)

	switch {
	case *asJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			log.Fatal(err)
		}
	case *missing:
		for _, e := range rep.Entries {
			if !e.Implemented && e.Scope != spec.ScopeOut {
				fmt.Printf("%-10s %-24s %s\n", e.Kind, e.Name, e.Page)
			}
		}
	default:
		kinds := make([]string, 0, len(rep.ByKind))
		for k := range rep.ByKind {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		for _, k := range kinds {
			c := rep.ByKind[k]
			fmt.Printf("%-10s %4d / %-4d implemented (%d excluded)\n", k, c.Implemented, c.InScope, c.Excluded)
		}
		if len(rep.Extra) > 0 {
			fmt.Printf("registered but not in the inventory: %v\n", rep.Extra)
		}
	}
}
