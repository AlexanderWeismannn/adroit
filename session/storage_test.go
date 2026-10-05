package session

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type memoryInstanceStorage struct{ instances json.RawMessage }

func (m *memoryInstanceStorage) SaveInstances(j json.RawMessage) error { m.instances = j; return nil }
func (m *memoryInstanceStorage) GetInstances() json.RawMessage         { return m.instances }
func (m *memoryInstanceStorage) DeleteAllInstances() error {
	m.instances = json.RawMessage("[]")
	return nil
}

// Deleting one session used to rebuild every other one through LoadInstances,
// which restores it: a tmux attach per session, and any session whose tmux had
// gone was rewritten as Paused on the way through. The records that are not
// being deleted must come back exactly as they were stored.
func TestDeleteInstanceLeavesTheOtherRecordsAsStored(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir()) // any tmux call lands on an empty private server
	t.Setenv("TMUX", "")
	dir := t.TempDir()
	records := []InstanceData{
		{Title: "doomed", Path: dir, Status: Running, NoWorktree: true, Program: "claude"},
		{Title: "neighbour", Path: dir, Status: Running, NoWorktree: true, Program: "claude", SessionID: "abc"},
	}
	raw, err := json.Marshal(records)
	require.NoError(t, err)
	state := &memoryInstanceStorage{instances: raw}
	storage, err := NewStorage(state)
	require.NoError(t, err)

	require.NoError(t, storage.DeleteInstance("doomed"))

	var left []InstanceData
	require.NoError(t, json.Unmarshal(state.instances, &left))
	require.Equal(t, records[1:], left)

	require.Error(t, storage.DeleteInstance("doomed"), "a missing title is still reported")
}
