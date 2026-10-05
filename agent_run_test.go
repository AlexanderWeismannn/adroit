package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexanderWeismannn/adroit/secrets"
)

type mapStore map[string]string

func (m mapStore) Get(name string) (string, error) {
	if v, ok := m[name]; ok {
		return v, nil
	}
	return "", secrets.ErrNotFound
}
func (m mapStore) Set(name, value string) error { m[name] = value; return nil }
func (m mapStore) Delete(name string) error     { delete(m, name); return nil }
func (m mapStore) Backend() string              { return "map" }

func writeProfiles(t *testing.T, profiles string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".adroit"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"default_program":"codex","profiles":` + profiles + `}`
	if !json.Valid([]byte(cfg)) {
		t.Fatalf("bad test config %s", cfg)
	}
	if err := os.WriteFile(filepath.Join(home, ".adroit", "config.json"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
}

func lookup(env []string, name string) (string, int) {
	var v string
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, name+"=") {
			v = strings.TrimPrefix(kv, name+"=")
			n++
		}
	}
	return v, n
}

// The stored key is what the agent sees -- once, and ahead of anything already
// exported, since getenv returns the first match and an appended copy would lose.
func TestAgentEnvInjectsTheStoredKey(t *testing.T) {
	writeProfiles(t, `[{"name":"codex","program":"codex","keys":["OPENAI_API_KEY","OPENAI_ORG_ID"]}]`)
	t.Setenv("OPENAI_API_KEY", "from-the-shell")
	t.Setenv("OPENAI_ORG_ID", "")
	store := mapStore{secrets.Key("codex", "OPENAI_API_KEY"): "from-settings"}

	env, missing := agentEnv("codex", store)
	v, n := lookup(env, "OPENAI_API_KEY")
	if v != "from-settings" || n != 1 {
		t.Fatalf("OPENAI_API_KEY = %q (%d entries), want the stored key exactly once", v, n)
	}
	if len(missing) != 1 || missing[0] != "OPENAI_ORG_ID" {
		t.Fatalf("missing = %v, want the key neither stored nor exported", missing)
	}
}

// A profile with no keys gets the environment as it is.
func TestAgentEnvLeavesAKeylessProfileAlone(t *testing.T) {
	writeProfiles(t, `[{"name":"claude","program":"claude"}]`)
	env, missing := agentEnv("claude", mapStore{})
	if len(missing) != 0 || len(env) != len(os.Environ()) {
		t.Fatalf("env changed for a profile with no keys: missing=%v", missing)
	}
}
