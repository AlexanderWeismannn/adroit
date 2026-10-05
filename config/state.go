package config

import (
	"encoding/json"
	"fmt"
	"github.com/AlexanderWeismannn/adroit/log"
	"os"
	"path/filepath"
	"time"
)

const (
	StateFileName     = "state.json"
	InstancesFileName = "instances.json"
)

// InstanceStorage handles instance-related operations
type InstanceStorage interface {
	// SaveInstances saves the raw instance data
	SaveInstances(instancesJSON json.RawMessage) error
	// GetInstances returns the raw instance data
	GetInstances() json.RawMessage
	// DeleteAllInstances removes all stored instances
	DeleteAllInstances() error
}

// AppState handles application-level state
type AppState interface {
	// GetHelpScreensSeen returns the bitmask of seen help screens
	GetHelpScreensSeen() uint32
	// SetHelpScreensSeen updates the bitmask of seen help screens
	SetHelpScreensSeen(seen uint32) error
	// GetDevStackInstance returns the title of the session the dev stack was
	// last pointed at
	GetDevStackInstance() string
	// SetDevStackInstance records the session the dev stack is pointed at
	SetDevStackInstance(title string) error
	// GetKilledSessions returns the killed sessions that can still be restored,
	// most recently killed first
	GetKilledSessions() []KilledSession
	// AddKilledSession records a session that was just killed
	AddKilledSession(killed KilledSession) error
}

// maxKilledSessions bounds the restore list. It is a record of accidents and
// second thoughts, not an archive: past a few dozen the picker is longer than
// the screen and the conversations at the bottom are older than the code they
// were about.
const maxKilledSessions = 50

// KilledSession is what is left of a session after it is killed: enough to start
// a new one whose conversation carries on where the old one stopped.
//
// Deliberately not the session itself. The worktree and the branch are gone by
// the time this is written -- that is what killing means -- and the only thing
// that survives is the Claude transcript, which lives outside the repository
// entirely. See session/resume.
type KilledSession struct {
	// Title is what the session was called, which a restore offers back as the
	// new session's name.
	Title string `json:"title"`
	// Branch is the branch it was on, shown so a row can be told apart from
	// another restore of the same ticket.
	Branch string `json:"branch,omitempty"`
	// RepoPath is the repository the session belonged to; the restored session
	// starts from the same one.
	RepoPath string `json:"repo_path"`
	// Program is the command the session ran, so a restored session runs the
	// same agent with the same flags.
	Program string `json:"program"`
	// SessionID is the Claude conversation id, which `claude --resume` takes.
	SessionID string `json:"session_id,omitempty"`
	// TranscriptPath is the conversation on disk, copied into the restored
	// session's own project directory when it starts.
	TranscriptPath string `json:"transcript_path,omitempty"`
	// Summary is the first thing the user said in that conversation, which is
	// what makes one row tell itself apart from another.
	Summary string `json:"summary,omitempty"`
	// KilledAt is when it was killed, rendered as an age in the picker.
	KilledAt time.Time `json:"killed_at"`
}

// StateManager combines instance storage and app state management
type StateManager interface {
	InstanceStorage
	AppState
}

// State represents the application state that persists between sessions
type State struct {
	// HelpScreensSeen is a bitmask tracking which help screens have been shown
	HelpScreensSeen uint32 `json:"help_screens_seen"`
	// Instances stores the serialized instance data as raw JSON
	InstancesData json.RawMessage `json:"instances"`
	// DevStackInstance is the session title the development stack was last
	// pointed at. The stack runs in a tmux session that outlives this process,
	// so without a record of WHICH session it belongs to, a restarted Adroit
	// finds a stack holding the ports and no way to say whose it is.
	DevStackInstance string `json:"dev_stack_instance,omitempty"`
	// KilledSessions are the sessions that have been killed and can still be
	// restored, most recently killed first.
	KilledSessions []KilledSession `json:"killed_sessions,omitempty"`

	// unsaveable is set on the defaults handed back for a state file that exists
	// but could not be read. Saving them would replace every session on record
	// with an empty list, so SaveState refuses instead.
	unsaveable bool
}

// DefaultState returns the default state
func DefaultState() *State {
	return &State{
		HelpScreensSeen: 0,
		InstancesData:   json.RawMessage("[]"),
	}
}

// LoadState loads the state from disk. If it cannot be done, we return the default state.
func LoadState() *State {
	configDir, err := GetConfigDir()
	if err != nil {
		log.ErrorLog.Printf("failed to get config directory: %v", err)
		return DefaultState()
	}

	statePath := filepath.Join(configDir, StateFileName)
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create and save default state if file doesn't exist
			defaultState := DefaultState()
			if saveErr := SaveState(defaultState); saveErr != nil {
				log.WarningLog.Printf("failed to save default state: %v", saveErr)
			}
			return defaultState
		}

		log.WarningLog.Printf("failed to get state file: %v", err)
		noteLoadWarning(fmt.Sprintf("could not read state.json (%v): no sessions are shown, and nothing will be saved until it can be read", err))
		state := DefaultState()
		state.unsaveable = true
		return state
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		// Starting from defaults means the next save overwrites the file, and the
		// file holds every session's worktree and conversation id. Keep it aside
		// first, so a damaged state costs a manual repair rather than the sessions.
		aside := fmt.Sprintf("%s.corrupt-%d", statePath, time.Now().Unix())
		if renameErr := os.Rename(statePath, aside); renameErr != nil {
			log.ErrorLog.Printf("failed to parse state file (%v), and could not move it aside: %v", err, renameErr)
			noteLoadWarning(fmt.Sprintf("state.json could not be parsed and could not be moved aside (%v); no sessions are shown", renameErr))
			state := DefaultState()
			state.unsaveable = true
			return state
		}
		log.ErrorLog.Printf("failed to parse state file (%v); kept it as %s and started empty", err, aside)
		noteLoadWarning(fmt.Sprintf("state.json could not be parsed; kept as %s and the session list starts empty", filepath.Base(aside)))
		return DefaultState()
	}

	return &state
}

// SaveState saves the state to disk
func SaveState(state *State) error {
	if state.unsaveable {
		return fmt.Errorf("not saving: state.json could not be read at startup, and saving now would replace it")
	}
	configDir, err := GetConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	statePath := filepath.Join(configDir, StateFileName)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Atomic: a crash or a VM stop part way through a plain write leaves a
	// truncated file, which the next load cannot parse.
	return writeFileAtomic(statePath, data, 0644)
}

// InstanceStorage interface implementation

// SaveInstances saves the raw instance data
func (s *State) SaveInstances(instancesJSON json.RawMessage) error {
	s.InstancesData = instancesJSON
	return SaveState(s)
}

// GetInstances returns the raw instance data
func (s *State) GetInstances() json.RawMessage {
	return s.InstancesData
}

// DeleteAllInstances removes all stored instances
func (s *State) DeleteAllInstances() error {
	s.InstancesData = json.RawMessage("[]")
	return SaveState(s)
}

// AppState interface implementation

// GetHelpScreensSeen returns the bitmask of seen help screens
func (s *State) GetHelpScreensSeen() uint32 {
	return s.HelpScreensSeen
}

// SetHelpScreensSeen updates the bitmask of seen help screens
func (s *State) SetHelpScreensSeen(seen uint32) error {
	s.HelpScreensSeen = seen
	return SaveState(s)
}

// GetDevStackInstance returns the session title the dev stack is pointed at.
func (s *State) GetDevStackInstance() string {
	return s.DevStackInstance
}

// SetDevStackInstance records the session the dev stack is pointed at. An empty
// title means no stack is running.
func (s *State) SetDevStackInstance(title string) error {
	s.DevStackInstance = title
	return SaveState(s)
}

// GetKilledSessions returns the restorable sessions, most recently killed first.
func (s *State) GetKilledSessions() []KilledSession {
	return s.KilledSessions
}

// AddKilledSession records a session that was just killed, at the front of the
// list.
//
// A conversation appears once however many times it is killed and restored: the
// id is the identity, and a second record of the same one would offer the user
// the same conversation twice with only the age to tell them apart.
func (s *State) AddKilledSession(killed KilledSession) error {
	kept := make([]KilledSession, 0, len(s.KilledSessions)+1)
	kept = append(kept, killed)
	for _, existing := range s.KilledSessions {
		if killed.SessionID != "" && existing.SessionID == killed.SessionID {
			continue
		}
		kept = append(kept, existing)
	}
	if len(kept) > maxKilledSessions {
		kept = kept[:maxKilledSessions]
	}
	s.KilledSessions = kept
	return SaveState(s)
}

// StoredWorktreePaths lists the worktree of every session in the state file. It
// reads the file and nothing else -- no defaults written, no damaged file moved
// aside -- so a check that only needs to know which worktrees are spoken for can
// run from anywhere without side effects.
func StoredWorktreePaths() ([]string, error) {
	configDir, err := GetConfigDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(configDir, StateFileName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	var records []struct {
		Worktree struct {
			WorktreePath string `json:"worktree_path"`
		} `json:"worktree"`
	}
	if len(state.InstancesData) > 0 {
		if err := json.Unmarshal(state.InstancesData, &records); err != nil {
			return nil, err
		}
	}
	paths := make([]string, 0, len(records))
	for _, r := range records {
		if r.Worktree.WorktreePath != "" {
			paths = append(paths, r.Worktree.WorktreePath)
		}
	}
	return paths, nil
}
