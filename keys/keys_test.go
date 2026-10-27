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
	session, _, _ := Offered(Ctx{HasInstance: true, Attention: true, PRDone: true})
	if len(session) < 3 || session[0] != KeyNextAttention || session[1] != KeyKill || session[2] != KeyEnter {
		t.Fatalf("session = %v, want waiting, kill, attach", session)
	}
	session, tools, app := Offered(Ctx{})
	if len(session) != 0 || len(tools) != 0 || len(app) != 6 || app[0] != KeyNew || app[1] != KeyPrompt {
		t.Fatalf("empty list: session %v tools %v app %v", session, tools, app)
	}
	ctx := Ctx{HasInstance: true, Started: true, Paused: true}
	session, _, _ = Offered(ctx)
	dim := Dimmed(ctx)
	for _, k := range session {
		if k == KeyEnter || k == KeySendPrompt {
			t.Errorf("a paused session offers %v", k)
		}
	}
	if len(dim) != 2 || dim[0] != KeyCheckout || dim[1] != KeyOpenPR {
		t.Errorf("a paused session dims %v, want checkout and the pull request", dim)
	}
}
