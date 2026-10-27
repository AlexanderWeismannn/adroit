package keys

import (
	"testing"
)

// Every key the dispatcher can see has a row in the table, and no key string is
// claimed twice: a second claim would make the menu highlight one entry while
// the dispatcher ran another.
func TestEveryGlobalKeyHasOneBinding(t *testing.T) {
	seen := map[string]KeyName{}
	for _, b := range Table {
		for _, k := range b.Keys {
			if prev, dup := seen[k]; dup {
				t.Errorf("%q is bound to both %v and %v", k, prev, b.Name)
			}
			seen[k] = b.Name
		}
		if _, ok := GlobalkeyBindings[b.Name]; !ok {
			t.Errorf("%v has no bubbles binding", b.Name)
		}
	}
	for k, name := range GlobalKeyStringsMap {
		if Lookup[name] == nil {
			t.Errorf("%q maps to %v, which is not in the table", k, name)
		}
	}
}

// A help row that names a binding shows that binding's key, so renaming a key
// in the table renames it on the card too.
func TestHelpRowsNameTableKeys(t *testing.T) {
	for _, section := range HelpSections {
		for _, line := range section.Lines {
			if line.Key != "" || line.Desc != "" {
				continue
			}
			b := Lookup[line.Name]
			if b == nil {
				t.Errorf("%s: help names %v, which is not in the table", section.Title, line.Name)
				continue
			}
			if b.Help == "" {
				t.Errorf("%s: %v is on the card but has no help text", section.Title, b.Name)
			}
		}
	}
}

// The menu's composition is a function of the facts, and the two cases the
// rows are reordered for are the ones worth pinning: the jump leads whenever it
// is offered, and kill leads new once the pull request is merged.
func TestOfferedLeadsWithWhatMattersMost(t *testing.T) {
	manage, _, _ := Offered(Ctx{HasInstance: true, Attention: true, PRDone: true})
	if len(manage) < 3 || manage[0] != KeyNextAttention || manage[1] != KeyKill || manage[2] != KeyNew {
		t.Fatalf("manage = %v, want jump, kill, new", manage)
	}
	manage, action, system := Offered(Ctx{})
	if len(action) != 0 || len(system) != 4 || manage[0] != KeyNew || manage[1] != KeyPrompt {
		t.Fatalf("empty list: manage %v action %v system %v", manage, action, system)
	}
	_, action, _ = Offered(Ctx{HasInstance: true, Started: true, Paused: true})
	for _, k := range action {
		if k == KeyEnter || k == KeyCheckout || k == KeySendPrompt {
			t.Errorf("a paused session offers %v", k)
		}
	}
}
