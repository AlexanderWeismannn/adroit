// Package boundary holds the one test that keeps the core out of the
// interface's dependencies. It has no code of its own.
package boundary

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// corePackages are the directories that know nothing about how Adroit is
// drawn: sessions, git, tmux, the dev stack, configuration, the daemon, the
// secrets store and the log. A Charm import in any of them would tie the core
// to one way of rendering it -- and the revamp's layout, theme and framework
// work must be able to touch the interface without reaching in here.
var corePackages = []string{"session", "config", "daemon", "secrets", "log", "cmd"}

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
