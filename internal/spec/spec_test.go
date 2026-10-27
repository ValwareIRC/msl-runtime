package spec

import "testing"

func TestInventory(t *testing.T) {
	inv, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Entries) == 0 {
		t.Fatal("inventory is empty; run tools/specgen")
	}
	seen := map[string]bool{}
	for _, e := range inv.Entries {
		if seen[e.Name] {
			t.Errorf("%s: duplicate entry", e.Name)
		}
		seen[e.Name] = true
		if len(e.Refs) == 0 {
			t.Errorf("%s: no help reference", e.Name)
		}
		// Only free-form topics may stay undecided; everything a script can
		// call or handle needs an in/out decision in spec/scope.json.
		if e.Kind != KindTopic && e.Scope.Scope == ScopeGuide {
			t.Errorf("%s: %s on a guide page needs a scope decision", e.Name, e.Kind)
		}
		if e.Scope.Scope == ScopeOut && e.Scope.Reason == "" {
			t.Errorf("%s: excluded without a reason", e.Name)
		}
	}
}

func TestKindOf(t *testing.T) {
	for name, want := range map[string]string{
		"$gettok":   KindIdentifier,
		"$":         KindTopic,
		"/sockopen": KindCommand,
		"on TEXT":   KindEvent,
		"raw":       KindTopic,
		"Variables": KindTopic,
	} {
		if got := KindOf(name); got != want {
			t.Errorf("KindOf(%q) = %s, want %s", name, got, want)
		}
	}
}
