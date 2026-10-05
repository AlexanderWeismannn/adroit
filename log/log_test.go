package log

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotateShiftsAnOversizedLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adroit.log")
	big := make([]byte, logRotateBytes)
	for _, f := range []string{path, path + ".1"} {
		if err := os.WriteFile(f, big, 0600); err != nil {
			t.Fatal(err)
		}
	}
	rotate(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the live log should have moved: %v", err)
	}
	for _, f := range []string{path + ".1", path + ".2"} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s missing: %v", f, err)
		}
	}

	small := filepath.Join(t.TempDir(), "adroit.log")
	_ = os.WriteFile(small, []byte("x"), 0600)
	rotate(small)
	if _, err := os.Stat(small); err != nil {
		t.Fatal("a small log must be left alone")
	}
}

func TestTestsDoNotWriteToTheRealLog(t *testing.T) {
	if filepath.Base(logFileName) == "adroit.log" {
		t.Fatal("a test binary must not log to the interface's own file")
	}
}
