package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// An unparseable state file used to be answered with defaults, and the next
// save wrote those defaults over it: every session, its worktree and its
// conversation id gone. The damaged file must survive for a manual repair.
func TestLoadStateKeepsAnUnparseableFileAside(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".adroit")
	require.NoError(t, os.MkdirAll(dir, 0755))
	damaged := []byte(`{"instances": [{"title": "half-writ`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, StateFileName), damaged, 0644))

	state := LoadState()
	require.JSONEq(t, "[]", string(state.GetInstances()))
	require.NoError(t, SaveState(state))

	aside, err := filepath.Glob(filepath.Join(dir, StateFileName+".corrupt-*"))
	require.NoError(t, err)
	require.Len(t, aside, 1)
	kept, err := os.ReadFile(aside[0])
	require.NoError(t, err)
	require.Equal(t, damaged, kept)
}

// Set aside is not the same as seen. The list came up empty with nothing on
// screen to say why -- every session apparently gone -- and the only record of
// what happened was a line in /tmp/adroit.log. The load now leaves a warning
// for the interface to show.
func TestAStateOrConfigThatCannotBeParsedIsReported(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".adroit")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, StateFileName), []byte(`{broken`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(`{broken`), 0644))
	TakeLoadWarnings()

	LoadState()
	LoadConfig()
	LoadConfig() // loaded twice on startup; said once
	warnings := TakeLoadWarnings()
	require.Len(t, warnings, 2)
	require.Contains(t, warnings[0], "state.json")
	require.Contains(t, warnings[1], "config.json")
	require.Empty(t, TakeLoadWarnings(), "taken once")
}

// A state file that exists but cannot be read -- permissions, an I/O error --
// was answered with defaults and nothing kept aside, so the next save wrote the
// empty defaults over every session on record. Such a state must refuse to save.
func TestAStateThatCouldNotBeReadIsNeverSavedOverTheFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads through permissions")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".adroit")
	require.NoError(t, os.MkdirAll(dir, 0755))
	path := filepath.Join(dir, StateFileName)
	original := []byte(`{"instances": [{"title": "precious"}]}`)
	require.NoError(t, os.WriteFile(path, original, 0o000))
	TakeLoadWarnings()

	state := LoadState()
	require.Error(t, state.SaveInstances([]byte(`[]`)), "saving over an unread file must be refused")
	require.NoError(t, os.Chmod(path, 0o644))
	onDisk, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, onDisk)
	require.NotEmpty(t, TakeLoadWarnings())
}
