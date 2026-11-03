// Package boundary holds the one test that keeps the core out of the
// interface's dependencies. It has no code of its own.
package boundary

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// corePackages are the directories that know nothing about how Adroit is
// drawn: sessions, git, tmux, the dev stack, configuration, the daemon, the
// secrets store and the log. A Charm import in any of them would tie the core
// to one way of rendering it -- and the revamp's layout, theme and framework
// work must be able to touch the interface without reaching in here.
var corePackages = []string{"session", "config", "daemon", "secrets", "log", "cmd"}

// Every colour the interface draws is a theme role. A literal colour outside
// theme/ is one a theme cannot change and a light terminal cannot adapt.
func TestNoColourOutsideTheme(t *testing.T) {
	root := filepath.Join("..", "..")
	literal := regexp.MustCompile(`lipgloss\.Color\(|lipgloss\.AdaptiveColor\{|lipgloss\.ANSIColor\(|lipgloss\.CompleteColor\{|"#[0-9a-fA-F]{6}"`)
	for _, dir := range []string{"ui", "app", "keys"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for n, line := range strings.Split(string(src), "\n") {
				if literal.MatchString(line) {
					t.Errorf("%s:%d names a colour; use a theme role", path, n+1)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
	}
}

func TestCoreImportsNoCharmPackage(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	for _, dir := range corePackages {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// Tests may measure with a Charm helper; what ships is what matters.
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range file.Imports {
				if strings.Contains(imp.Path.Value, "charmbracelet") {
					t.Errorf("%s imports %s: the core must not depend on the interface's framework",
						path, imp.Path.Value)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
	}
}
