package session

import (
	"encoding/json"
	"fmt"
	"github.com/AlexanderWeismannn/adroit/config"
	"time"
)

// InstanceData represents the serializable data of an Instance
type InstanceData struct {
	Title     string    `json:"title"`
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	Status    Status    `json:"status"`
	Height    int       `json:"height"`
	Width     int       `json:"width"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// LastActiveAt is when the session was last doing something, which the list
	// renders as how long it has been quiet. Persisted so a restart of cs does
	// not reset every row's age to nothing; absent from an older state file,
	// where the zero value correctly means "unknown".
	LastActiveAt time.Time `json:"last_active_at"`
	AutoYes      bool      `json:"auto_yes"`

	// NoWorktree marks a session that runs directly in the repository rather than
	// in a worktree of its own. Persisted explicitly rather than inferred from an
	// empty Worktree, because "no worktree was ever made" and "the worktree data
	// failed to write" must not look the same on reload: the second would rebuild
	// a GitWorktree pointing at the repository root, and killing that session
	// would `git worktree remove -f` and `git branch -D` the user's main checkout.
	NoWorktree bool `json:"no_worktree"`

	Program string `json:"program"`
	// SessionID is the Claude conversation the session owns, so that resuming it
	// after the tmux server has died brings the conversation back rather than
	// starting an empty one. Absent from a state file written by an older build,
	// where the zero value correctly means "we never named it".
	SessionID string `json:"session_id,omitempty"`

	Worktree  GitWorktreeData `json:"worktree"`
	DiffStats DiffStatsData   `json:"diff_stats"`
}

// GitWorktreeData represents the serializable data of a GitWorktree
type GitWorktreeData struct {
	RepoPath      string `json:"repo_path"`
	WorktreePath  string `json:"worktree_path"`
	SessionName   string `json:"session_name"`
	BranchName    string `json:"branch_name"`
	BaseCommitSHA string `json:"base_commit_sha"`
	// BaseBranch is the branch the session forked from. Absent from a state file
	// written by an older build, where diff bases were frozen to BaseCommitSHA.
	BaseBranch       string `json:"base_branch,omitempty"`
	IsExistingBranch bool   `json:"is_existing_branch"`
}

// DiffStatsData represents the serializable data of a DiffStats
type DiffStatsData struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	// Content is legacy: it is no longer written and no longer read back. Kept so
	// a state file written by an older build still unmarshals cleanly.
	Content string `json:"content,omitempty"`
}

// Storage handles saving and loading instances using the state interface
type Storage struct {
	state config.InstanceStorage
}

// NewStorage creates a new storage instance
func NewStorage(state config.InstanceStorage) (*Storage, error) {
	return &Storage{
		state: state,
	}, nil
}

// SaveInstances saves the list of instances to disk
func (s *Storage) SaveInstances(instances []*Instance) error {
	// Convert instances to InstanceData
	data := make([]InstanceData, 0)
	for _, instance := range instances {
		if instance.Started() {
			data = append(data, instance.ToInstanceData())
		}
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal instances: %w", err)
	}

	return s.state.SaveInstances(jsonData)
}

// LoadInstances loads the list of instances from disk
func (s *Storage) LoadInstances() ([]*Instance, error) {
	jsonData := s.state.GetInstances()

	var instancesData []InstanceData
	if err := json.Unmarshal(jsonData, &instancesData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal instances: %w", err)
	}

	instances := make([]*Instance, len(instancesData))
	for i, data := range instancesData {
		instance, err := FromInstanceData(data)
		if err != nil {
			return nil, fmt.Errorf("failed to create instance %s: %w", data.Title, err)
		}
		instances[i] = instance
	}

	return instances, nil
}

// loadRecords reads the stored sessions as plain data. Delete and update work on
// these rather than on LoadInstances: building an Instance is not free -- it
// restores the session, which re-attaches a tmux client to every one of them --
// and neither operation needs anything but the records.
func (s *Storage) loadRecords() ([]InstanceData, error) {
	var records []InstanceData
	if err := json.Unmarshal(s.state.GetInstances(), &records); err != nil {
		return nil, fmt.Errorf("failed to unmarshal instances: %w", err)
	}
	return records, nil
}

func (s *Storage) saveRecords(records []InstanceData) error {
	jsonData, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("failed to marshal instances: %w", err)
	}
	return s.state.SaveInstances(jsonData)
}

// DeleteInstance removes an instance from storage
func (s *Storage) DeleteInstance(title string) error {
	records, err := s.loadRecords()
	if err != nil {
		return fmt.Errorf("failed to load instances: %w", err)
	}

	kept := make([]InstanceData, 0, len(records))
	for _, record := range records {
		if record.Title != title {
			kept = append(kept, record)
		}
	}
	if len(kept) == len(records) {
		return fmt.Errorf("instance not found: %s", title)
	}
	return s.saveRecords(kept)
}

// UpdateInstance updates an existing instance in storage
func (s *Storage) UpdateInstance(instance *Instance) error {
	records, err := s.loadRecords()
	if err != nil {
		return fmt.Errorf("failed to load instances: %w", err)
	}

	data := instance.ToInstanceData()
	for i := range records {
		if records[i].Title == data.Title {
			records[i] = data
			return s.saveRecords(records)
		}
	}
	return fmt.Errorf("instance not found: %s", data.Title)
}

// DeleteAllInstances removes all stored instances
func (s *Storage) DeleteAllInstances() error {
	return s.state.DeleteAllInstances()
}
