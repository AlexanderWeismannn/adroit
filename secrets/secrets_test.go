package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// The fallback file is the store on WSL and headless Linux, so it is where a key
// most often lives: it must round-trip, and never be readable by anyone else.
func TestFileStoreKeepsKeysPrivate(t *testing.T) {
	home := withHome(t)
	f := &fileStore{}

	if _, err := f.Get("codex/OPENAI_API_KEY"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on an empty store = %v, want ErrNotFound", err)
	}
	if err := f.Set("codex/OPENAI_API_KEY", "sk-test-123=with=equals"); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("gemini/GEMINI_API_KEY", "g-456"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Get("codex/OPENAI_API_KEY"); err != nil || got != "sk-test-123=with=equals" {
		t.Fatalf("Get = %q, %v", got, err)
	}

	info, err := os.Stat(filepath.Join(home, ".adroit", FileName))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("secrets file mode = %o, want 600", perm)
	}

	if err := f.Delete("codex/OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("codex/OPENAI_API_KEY"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted key still readable: %v", err)
	}
	if got, _ := f.Get("gemini/GEMINI_API_KEY"); got != "g-456" {
		t.Fatalf("deleting one key disturbed another: %q", got)
	}

	if err := f.Set("x/Y", "line\nbreak"); err == nil {
		t.Fatal("a value with a line break would corrupt the file; Set must refuse it")
	}
}

// A keyring that refuses -- locked, no daemon -- must not lose the key: it lands
// in the file, and reads still find it.
func TestFallbackKeepsAKeyTheKeyringRefused(t *testing.T) {
	withHome(t)
	store := &fallback{primary: refusing{}, file: &fileStore{}}
	if err := store.Set("aider/ANTHROPIC_API_KEY", "a-1"); err != nil {
		t.Fatalf("Set = %v, want the file to take it", err)
	}
	if got, err := store.Get("aider/ANTHROPIC_API_KEY"); err != nil || got != "a-1" {
		t.Fatalf("Get = %q, %v", got, err)
	}
}

type refusing struct{}

func (refusing) Get(string) (string, error) { return "", errors.New("locked") }
func (refusing) Set(string, string) error   { return errors.New("locked") }
func (refusing) Delete(string) error        { return errors.New("locked") }
func (refusing) Backend() string            { return "refusing" }

// security(1) is the one backend whose CLI takes the value as an argument if
// asked to; the store must feed it on stdin instead, or every process on the
// machine could read the key out of ps while it runs.
func TestKeychainNeverPutsTheKeyOnACommandLine(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\n" +
		"echo \"ARGS: $*\" >> " + log + "\n" +
		"cat >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(dir, "security"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	const value = "sk-very-secret-value"
	if err := (keychain{}).Set("codex/OPENAI_API_KEY", value); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "ARGS:") && strings.Contains(line, value) {
			t.Fatalf("the key reached security's argv: %q", line)
		}
	}
	if !strings.Contains(string(data), value) {
		t.Fatalf("the key never reached security at all: %q", data)
	}
}

func TestMaskHidesTheKey(t *testing.T) {
	if got := Mask("sk-proj-abcdefghijklmnop1234"); got != "sk-••••••1234" {
		t.Fatalf("Mask = %q", got)
	}
	if got := Mask("short"); strings.Contains(got, "s") {
		t.Fatalf("a short key must be fully hidden, got %q", got)
	}
}
