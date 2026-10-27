// Package spec holds the inventory of everything mIRC 7.85 documents for
// scripting, so coverage of the runtime can be measured against it.
package spec

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

// Entry kinds.
const (
	KindIdentifier = "identifier"
	KindCommand    = "command"
	KindEvent      = "event"
	KindTopic      = "topic"
)

// Scope values.
const (
	ScopeIn    = "in"
	ScopeOut   = "out"
	ScopeGuide = "guide"
)

// Inventory is the generated contents of inventory.json.
type Inventory struct {
	Source  string  `json:"source"`
	Entries []Entry `json:"entries"`
}

// Entry is one help-index keyword, with every place it is documented.
type Entry struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Scope Scope  `json:"scope"`
	Refs  []Ref  `json:"refs"`
}

// Scope says whether the runtime must implement an entry, and why not.
type Scope struct {
	Scope  string `json:"scope"`
	Reason string `json:"reason,omitempty"`
}

// Ref locates an entry in the help file.
type Ref struct {
	Page   string `json:"page"`
	Anchor string `json:"anchor,omitempty"`
	Syntax string `json:"syntax,omitempty"`
}

// KindOf classifies an index keyword by its spelling.
func KindOf(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(name, "$") && len(name) > 1:
		return KindIdentifier
	case strings.HasPrefix(name, "/") && len(name) > 1:
		return KindCommand
	case strings.HasPrefix(lower, "on ") || strings.HasPrefix(lower, "raw ") || strings.HasPrefix(lower, "ctcp "):
		return KindEvent
	}
	return KindTopic
}

//go:embed inventory.json
var inventoryJSON []byte

var (
	loadOnce sync.Once
	loaded   Inventory
	loadErr  error
)

// Load returns the embedded inventory.
func Load() (Inventory, error) {
	loadOnce.Do(func() { loadErr = json.Unmarshal(inventoryJSON, &loaded) })
	return loaded, loadErr
}
