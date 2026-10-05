package session

import (
	"errors"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session/ci"
	"github.com/AlexanderWeismannn/adroit/session/git"
	"github.com/AlexanderWeismannn/adroit/session/resume"
	"github.com/AlexanderWeismannn/adroit/session/tmux"
	"github.com/AlexanderWeismannn/adroit/session/upstream"
	"path/filepath"

	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/atotto/clipboard"
)

type Status int

const (
	// Running is the status when the instance is running and claude is working.
	Running Status = iota
	// Ready is if the claude instance is ready to be interacted with (waiting for user input).
	Ready
	// Loading is if the instance is loading (if we are starting it up or something).
	Loading
	// Paused is if the instance is paused (worktree removed but branch preserved).
	Paused
)

// Activity is what a session's agent is doing right now, which is a different
// question from Status: Status is about the session's own lifecycle -- started,
// parked, being rebuilt -- and survives a restart, while Activity describes this
// moment and does not.
//
// It exists because the pane-content hash cannot answer the question. The hash
// says "these two captures differ", and reads a still pane as a finished one,
// which is wrong in exactly the case that matters.
type Activity int

const (
	// ActivityIdle is the agent having ended its turn with nothing of its own
	// outstanding: the session is waiting on you.
	ActivityIdle Activity = iota
	// ActivityWorking is the agent mid-turn.
	ActivityWorking
	// ActivityShell is the agent having ended its turn but left a background
	// shell running, so it will wake up and carry on by itself. It looks finished
	// and is not, which is the whole reason this type is here.
	ActivityShell
	// ActivityNeedsInput is the agent stopped mid-turn on a question only you can
	// answer -- a permission prompt, a plan to approve, a menu of options. Not
	// finished, since answering it resumes the same turn, and not working, since
	// nothing happens until you do.
	ActivityNeedsInput
)

// Instance is a running instance of claude code.
type Instance struct {
	// branchCopied is whether the last pause put the branch on the clipboard.
	branchCopied bool
	// Title is the title of the instance.
	Title string
	// Path is the path to the workspace.
	Path string
	// Branch is the branch of the instance.
	Branch string
	// Status is the status of the instance.
	Status Status
	// Program is the program to run in the instance.
	Program string
	// SessionID is the Claude conversation this session owns.
	//
	// Adroit names the conversation when it starts one, with
	// `claude --session-id`, rather than reading the id back afterwards. That is
	// what makes a conversation survivable: when the tmux server dies -- a
	// reboot, a crash, `tmux kill-server`, or the WSL VM stopping because the
	// last terminal window was closed -- the worktree and the branch are still on
	// disk but the agent is not, and resuming used to start a brand new Claude in
	// the pane. With an id recorded, resuming runs `claude --resume` and the
	// conversation comes back.
	//
	// It cannot be discovered after the fact. Claude files transcripts by working
	// directory, so the sessions that run without a worktree of their own share
	// one project folder, and "the newest transcript there" is whichever of them
	// typed last. Empty for a session started by an older build, and for a
	// program that is not Claude.
	SessionID string
	// Height is the height of the instance.
	Height int
	// Width is the width of the instance.
	Width int
	// CreatedAt is the time the instance was created.
	CreatedAt time.Time
	// UpdatedAt is the time the instance was last updated.
	UpdatedAt time.Time
	// AutoYes is true if the instance should automatically press enter when prompted.
	AutoYes bool
	// Prompt is the initial prompt to pass to the instance on startup
	Prompt string

	// DiffStats stores the current git diff statistics
	diffStats *git.DiffStats

	// diffStatsAt is when diffStats was last recomputed, and diffStatsHasContent
	// whether that computation loaded the full diff text or only the counts.
	//
	// Both exist to keep the metadata tick from re-deriving a diff that cannot
	// have changed. Computing one costs two git subprocesses -- an `add -N` walk
	// of the whole worktree, then the diff itself -- and at 12ms and 8ms measured
	// here that was the single most expensive thing the interface did, twice a
	// second per session, whether or not a byte had moved.
	diffStatsAt         time.Time
	diffStatsHasContent bool

	// untrackedAt is when untracked files were last folded into the counts. The
	// walk that finds them is the expensive half of a cheap diff, and a new
	// untracked file is rare next to the rate the counts are taken at.
	untrackedAt time.Time

	// currentBranchAt is when currentBranch was last resolved. A session's
	// checked-out branch changes approximately never and the value is
	// display-only, so it does not need a subprocess every tick.
	currentBranchAt time.Time

	// ciStatus is the last known CI verdict for Branch. Deliberately not
	// persisted: a verdict from a previous run of cs would be arbitrarily stale,
	// and a fresh one arrives within a tick or two of startup.
	ciStatus ci.Status

	// upstreamStatus is how Branch stands against its remote counterpart. Not
	// persisted, for the same reason: a count from a previous run describes a
	// remote that has since moved.
	upstreamStatus upstream.Status

	// activity is what this session's agent is doing, read off its own interface
	// rather than inferred from whether the pane changed. Not persisted, for the
	// reason given on Activity: a recorded value would describe a session that is
	// no longer running.
	activity Activity

	// backgroundShells is how many shells the agent has left running behind it.
	backgroundShells int

	// lastActiveAt is the last moment this session was doing something: an agent
	// mid-turn, a background shell of its own, or -- for a program whose
	// interface we cannot read -- a pane whose content moved.
	//
	// Kept rather than derived from doneAt, which is cleared the moment a finish
	// is acknowledged, and from UpdatedAt, which is stamped when the session is
	// written to disk and so says when cs last saved, not when the session last
	// did anything. What the row wants is "quiet for how long", and only a
	// timestamp that survives the acknowledgement can answer it.
	lastActiveAt time.Time

	// doneAt is when this session last went idle. The list marks a row that just
	// finished, which needs the moment of the transition and not merely the fact
	// of being idle -- a column of green dots cannot tell you which one changed
	// while you were reading another pane. Zero while anything is still running,
	// and once the finish has been acknowledged.
	doneAt time.Time

	// currentBranch is the branch the worktree actually has checked out, which is
	// not always Branch: renaming a branch, or checking out another one, from
	// inside a worktree is ordinary. Display-only, and deliberately not persisted
	// — it is re-resolved a tick after startup, and a stale value would outrank
	// the recorded name it exists to correct.
	currentBranch string

	// selectedBranch is the existing branch to start on (empty = new branch from HEAD)
	selectedBranch string

	// noWorktree runs the session directly in Path instead of giving it a worktree
	// and a branch of its own -- a scratch terminal on the repository as it stands.
	//
	// Such a session's gitWorktree stays nil for its whole life, which is what
	// makes it safe: Kill and every other worktree operation is nil-guarded, so
	// none of them can reach the user's real checkout. Nothing may construct a
	// GitWorktree rooted at Path for one of these.
	noWorktree bool

	// resumedConversation records whether the last start picked an existing
	// Claude conversation back up. Not persisted: it describes one start.
	resumedConversation bool

	// onWorkspaceReady is run once the session's directory exists and before the
	// program starts in it. Not persisted: it belongs to one particular start,
	// and a session reloaded from storage has already had it.
	onWorkspaceReady func(dir string) error

	// The below fields are initialized upon calling Start().

	started bool
	// tmuxSession is the tmux session for the instance.
	tmuxSession *tmux.TmuxSession
	// gitWorktree is the git worktree for the instance.
	gitWorktree *git.GitWorktree
}

// ToInstanceData converts an Instance to its serializable form
func (i *Instance) ToInstanceData() InstanceData {
	data := InstanceData{
		Title:        i.Title,
		LastActiveAt: i.lastActiveAt,
		Path:         i.Path,
		Branch:       i.Branch,
		Status:       i.Status,
		Height:       i.Height,
		Width:        i.Width,
		CreatedAt:    i.CreatedAt,
		UpdatedAt:    time.Now(),
		Program:      i.Program,
		SessionID:    i.SessionID,
		AutoYes:      i.AutoYes,
	}

	data.NoWorktree = i.noWorktree

	// Only include worktree data if gitWorktree is initialized
	if i.gitWorktree != nil {
		data.Worktree = GitWorktreeData{
			RepoPath:         i.gitWorktree.GetRepoPath(),
			WorktreePath:     i.gitWorktree.GetWorktreePath(),
			SessionName:      i.Title,
			BranchName:       i.gitWorktree.GetBranchName(),
			BaseCommitSHA:    i.gitWorktree.GetBaseCommitSHA(),
			BaseBranch:       i.gitWorktree.GetBaseBranch(),
			IsExistingBranch: i.gitWorktree.IsExistingBranch(),
		}
	}

	// Only include diff stats if they exist
	if i.diffStats != nil {
		// Counts only. The content is a whole diff -- megabytes on a large
		// branch -- and it is written to be discarded: the first metadata tick
		// after a reload recomputes it, and a diff loaded from a previous run
		// describes a worktree that has since moved.
		data.DiffStats = DiffStatsData{
			Added:   i.diffStats.Added,
			Removed: i.diffStats.Removed,
		}
	}

	return data
}

// FromInstanceData creates a new Instance from serialized data
func FromInstanceData(data InstanceData) (*Instance, error) {
	instance := &Instance{
		Title:        data.Title,
		Path:         data.Path,
		Branch:       data.Branch,
		Status:       data.Status,
		Height:       data.Height,
		Width:        data.Width,
		CreatedAt:    data.CreatedAt,
		UpdatedAt:    data.UpdatedAt,
		lastActiveAt: data.LastActiveAt,
		Program:      data.Program,
		SessionID:    data.SessionID,
		noWorktree:   data.NoWorktree,
		// Content is deliberately not restored, and no longer written -- see
		// ToInstanceData. A state file from an older build may still carry one.
		diffStats: &git.DiffStats{
			Added:   data.DiffStats.Added,
			Removed: data.DiffStats.Removed,
		},
	}

	// A worktree-less session keeps a nil worktree across reload. Building one
	// from the stored zero value would point it at "" and, worse, invite every
	// worktree operation to run against a path derived from the repository.
	if !data.NoWorktree {
		instance.gitWorktree = git.NewGitWorktreeFromStorage(
			data.Worktree.RepoPath,
			data.Worktree.WorktreePath,
			data.Worktree.SessionName,
			data.Worktree.BranchName,
			data.Worktree.BaseCommitSHA,
			data.Worktree.BaseBranch,
			data.Worktree.IsExistingBranch,
		)
	}

	if instance.Paused() {
		instance.started = true
		instance.tmuxSession = tmux.NewTmuxSession(instance.Title, instance.Program)
	} else {
		if err := instance.Start(false); err != nil {
			return nil, err
		}
	}

	return instance, nil
}

// Options for creating a new instance
type InstanceOptions struct {
	// Title is the title of the instance.
	Title string
	// Path is the path to the workspace.
	Path string
	// Program is the program to run in the instance (e.g. "claude", "aider --model ollama_chat/gemma3:1b")
	Program string
	// If AutoYes is true, then
	AutoYes bool
	// Branch is an existing branch name to start the session on (empty = new branch from HEAD)
	Branch string
	// NoWorktree runs the session in Path itself, with no worktree and no branch.
	NoWorktree bool
}

func NewInstance(opts InstanceOptions) (*Instance, error) {
	t := time.Now()

	// Convert path to absolute
	absPath, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	return &Instance{
		Title:          opts.Title,
		Status:         Ready,
		Path:           absPath,
		Program:        opts.Program,
		Height:         0,
		Width:          0,
		CreatedAt:      t,
		UpdatedAt:      t,
		AutoYes:        false,
		selectedBranch: opts.Branch,
		noWorktree:     opts.NoWorktree,
	}, nil
}

// RepoName is the name of the repository this session belongs to.
//
// Answerable before the session starts, from the directory it will run in: the
// list registers the name as soon as the session is named, which is before its
// worktree exists, and used to get only an error back.
func (i *Instance) RepoName() (string, error) {
	if i.gitWorktree != nil {
		return i.gitWorktree.GetRepoName(), nil
	}
	root, err := git.RepoRoot(i.Path)
	if err != nil {
		return "", fmt.Errorf("cannot get repo name: %w", err)
	}
	return filepath.Base(root), nil
}

// RepoPath is the root of the repository this session belongs to -- the main
// checkout, never the worktree, so every session of a repository resolves to the
// same per-repository configuration.
//
// Unlike RepoName it does not require the session to have started. It is read to
// decide what `d` would run, which the interface answers for a session that has
// not started yet, and the path the session was created from is already right.
func (i *Instance) RepoPath() string {
	if i.gitWorktree != nil {
		return i.gitWorktree.GetRepoPath()
	}
	return i.Path
}

func (i *Instance) SetStatus(status Status) {
	// Running is the only status the content hash can assert on its own, and for
	// a program whose interface cannot be parsed it is the sole evidence the
	// session is doing anything. Stamped here so those sessions age too.
	if status == Running {
		i.lastActiveAt = time.Now()
	}
	i.Status = status
}

// LastActiveAt is when this session was last doing something, or the zero time
// if it has done nothing since cs started and nothing was restored.
func (i *Instance) LastActiveAt() time.Time {
	return i.lastActiveAt
}

// SetSelectedBranch sets the branch to use when starting the instance.
func (i *Instance) SetSelectedBranch(branch string) {
	i.selectedBranch = branch
}

// SetNoWorktree marks the instance as running in Path itself, with no worktree
// and no branch of its own.
func (i *Instance) SetNoWorktree(noWorktree bool) {
	i.noWorktree = noWorktree
}

// NoWorktree reports whether this session runs directly in the repository rather
// than in a worktree of its own. Such a session has no branch, so the operations
// that act on one -- push, checkout, pause, resume, diff, the CI badge -- do not
// apply to it, and callers gate on this rather than on a nil worktree so the
// answer is the same before the session has started.
func (i *Instance) NoWorktree() bool {
	return i.noWorktree
}

// firstTimeSetup is true if this is a new instance. Otherwise, it's one loaded from storage.
func (i *Instance) Start(firstTimeSetup bool) error {
	if i.Title == "" {
		return fmt.Errorf("instance title cannot be empty")
	}

	var tmuxSession *tmux.TmuxSession
	if i.tmuxSession != nil {
		// Use existing tmux session (useful for testing)
		tmuxSession = i.tmuxSession
	} else {
		// Create new tmux session
		tmuxSession = tmux.NewTmuxSession(i.Title, i.Program)
	}
	i.tmuxSession = tmuxSession

	if firstTimeSetup && !i.noWorktree {
		if i.selectedBranch != "" {
			gitWorktree, err := git.NewGitWorktreeFromBranch(i.Path, i.selectedBranch, i.Title)
			if err != nil {
				return fmt.Errorf("failed to create git worktree from branch: %w", err)
			}
			i.gitWorktree = gitWorktree
			i.Branch = i.selectedBranch
		} else {
			gitWorktree, branchName, err := git.NewGitWorktree(i.Path, i.Title)
			if err != nil {
				return fmt.Errorf("failed to create git worktree: %w", err)
			}
			i.gitWorktree = gitWorktree
			i.Branch = branchName
		}
	}

	// Setup error handler to cleanup resources on any error
	var setupErr error
	defer func() {
		if setupErr != nil {
			if cleanupErr := i.Kill(); cleanupErr != nil {
				setupErr = fmt.Errorf("%v (cleanup error: %v)", setupErr, cleanupErr)
			}
		} else {
			i.started = true
		}
	}()

	if !firstTimeSetup {
		// Reuse existing session. If the tmux server died since we last ran (reboot,
		// crash, `tmux kill-server`), the session is gone but the worktree and branch
		// are still on disk. Park the instance as Paused so Resume can rebuild it.
		// Reporting an error here would be worse than useless: LoadInstances aborts on
		// the first failure, so a single dead session would hide every other instance.
		if err := tmuxSession.Restore(); err != nil {
			if errors.Is(err, tmux.ErrSessionNotFound) {
				log.WarningLog.Printf(
					"tmux session for %q no longer exists; pausing instance so it can be resumed", i.Title)
				i.SetStatus(Paused)
				return nil
			}
			// Alive but unreachable -- most often a PTY that cannot be opened.
			// Failing here failed LoadInstances, and Adroit exited before drawing
			// anything, on every relaunch, over one session. Parked, it is a row
			// the user can resume once whatever blocked it has cleared.
			log.ErrorLog.Printf("could not restore %q, pausing it: %v", i.Title, err)
			i.SetStatus(Paused)
			return nil
		}
	} else if i.noWorktree {
		// No worktree to set up, and none to clean up if tmux fails: the session
		// runs in the repository as the user left it.
		if err := i.prepareWorkspace(i.Path); err != nil {
			setupErr = err
			return setupErr
		}
		if err := i.startTmux(i.Path); err != nil {
			setupErr = fmt.Errorf("failed to start new session: %w", err)
			return setupErr
		}
	} else {
		// Setup git worktree first
		if err := i.gitWorktree.Setup(); err != nil {
			setupErr = fmt.Errorf("failed to setup git worktree: %w", err)
			return setupErr
		}

		// Anything the program needs to find in the worktree has to be put there
		// now: the next line starts it, and from then on it is the program's
		// directory, not ours.
		if err := i.prepareWorkspace(i.gitWorktree.GetWorktreePath()); err != nil {
			if cleanupErr := i.gitWorktree.AbandonSetup(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			setupErr = err
			return setupErr
		}

		// Create new session
		if err := i.startTmux(i.gitWorktree.GetWorktreePath()); err != nil {
			// Cleanup git worktree if tmux session creation fails
			if cleanupErr := i.gitWorktree.AbandonSetup(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			setupErr = fmt.Errorf("failed to start new session: %w", err)
			return setupErr
		}
	}

	i.SetStatus(Running)

	return nil
}

// startTmux starts the program in dir, deciding first whether it is opening a
// new conversation or picking an existing one back up.
//
// Every tmux start goes through here. The decision cannot live in Start alone:
// resume starts a session too, and resume is the case the whole mechanism
// exists for.
func (i *Instance) startTmux(dir string) error {
	i.tmuxSession.SetLaunchCommand(i.launchCommandFor(dir))
	return i.tmuxSession.Start(dir)
}

// launchCommandFor is the command line to start the program in dir with, and
// records the conversation it will be talking to.
//
// Three outcomes: a program that is not Claude, or one already aimed at a
// conversation by its caller, is run as it stands; a session id whose transcript
// has something in it is resumed; anything else starts a new conversation under
// an id chosen here, so that the next start can resume it.
func (i *Instance) launchCommandFor(dir string) string {
	i.resumedConversation = false

	if !resume.IsClaude(i.Program) || resume.CarriesSessionFlag(i.Program) {
		return i.Program
	}

	if resume.Exists(dir, i.SessionID) {
		i.resumedConversation = true
		return resume.Command(i.Program, i.SessionID)
	}

	// No id recorded, but there may still be a conversation to pick up: sessions
	// created before Adroit started naming them have one on disk and nothing
	// pointing at it. Adopted only when the directory holds exactly one, which
	// resume.Sole is what enforces -- a guess here would hand one session's
	// conversation to another.
	if i.SessionID == "" {
		if transcript, ok := resume.Sole(dir); ok {
			log.InfoLog.Printf("adopting the conversation already in %s for %q", dir, i.Title)
			i.SessionID = transcript.SessionID
			i.resumedConversation = true
			return resume.Command(i.Program, i.SessionID)
		}
	}

	// A fresh id even when one is already recorded. `claude --session-id` refuses
	// an id it has already used, and a transcript that is present but empty -- a
	// session that started and was never spoken to -- counts as used. Reusing it
	// would fail in the pane, which is a worse outcome than losing a conversation
	// that has nothing in it.
	id, err := resume.NewSessionID()
	if err != nil {
		log.WarningLog.Printf(
			"could not name a conversation for %q, so it will not be resumable: %v", i.Title, err)
		i.SessionID = ""
		return i.Program
	}
	i.SessionID = id
	return resume.StartCommand(i.Program, id)
}

// ResumedConversation reports whether the last start picked a conversation back
// up rather than opening a new one.
//
// Worth saying out loud: resuming happens on the path taken after a crash or a
// reboot, where the user's expectation is the opposite -- they have been told
// for years that a killed agent is a lost one -- and a resumed session looks
// identical to a fresh one until they scroll.
func (i *Instance) ResumedConversation() bool {
	return i.resumedConversation
}

// SetOnWorkspaceReady registers work to do in the session's directory after it
// exists and before the program starts in it.
//
// One caller today: a restored session, which has to copy the killed session's
// Claude transcript into the new worktree's project directory. That cannot
// happen earlier -- the worktree path is only decided during Start, and carries
// a timestamp so it is never the same twice -- and it cannot happen later, since
// by then the program has already looked for the conversation and not found it.
func (i *Instance) SetOnWorkspaceReady(fn func(dir string) error) {
	i.onWorkspaceReady = fn
}

// prepareWorkspace runs the hook, if there is one. Its error fails the start:
// the hook exists to put something in place that the program is about to be told
// to use, so starting anyway would produce a session that fails in the pane with
// nothing to say why.
func (i *Instance) prepareWorkspace(dir string) error {
	if i.onWorkspaceReady == nil {
		return nil
	}
	if err := i.onWorkspaceReady(dir); err != nil {
		return fmt.Errorf("failed to prepare the session directory: %w", err)
	}
	return nil
}

// Kill terminates the instance and cleans up all resources
func (i *Instance) Kill() error {
	if !i.started {
		// If instance was never started, just return success
		return nil
	}

	var errs []error

	// Always try to cleanup both resources, even if one fails
	// Clean up tmux session first since it's using the git worktree
	if i.tmuxSession != nil {
		if err := i.tmuxSession.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close tmux session: %w", err))
		}
	}

	// Then clean up git worktree
	if i.gitWorktree != nil {
		if err := i.gitWorktree.Cleanup(); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup git worktree: %w", err))
		}
	}

	return i.combineErrors(errs)
}

// combineErrors combines multiple errors into a single error
func (i *Instance) combineErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}

	errMsg := "multiple cleanup errors occurred:"
	for _, err := range errs {
		errMsg += "\n  - " + err.Error()
	}
	return fmt.Errorf("%s", errMsg)
}

func (i *Instance) Preview() (string, error) {
	if !i.started || i.Status == Paused {
		return "", nil
	}
	return i.tmuxSession.CapturePaneContent()
}

// PollPane captures this session's pane once and reports everything the capture
// says. A session with no pane -- unstarted, or parked -- reports nothing.
func (i *Instance) PollPane() tmux.PaneState {
	if !i.started || i.Status == Paused {
		return tmux.PaneState{}
	}
	return i.tmuxSession.PollPane()
}

// CheckAndHandleTrustPrompt checks for and dismisses the trust prompt for supported programs.
func (i *Instance) CheckAndHandleTrustPrompt() bool {
	if !i.started || i.tmuxSession == nil {
		return false
	}
	program := i.Program
	if !strings.HasSuffix(program, tmux.ProgramClaude) &&
		!strings.HasSuffix(program, tmux.ProgramAider) &&
		!strings.HasSuffix(program, tmux.ProgramGemini) {
		return false
	}
	return i.tmuxSession.CheckAndHandleTrustPrompt()
}

// TapEnter sends an enter key press to the tmux session if AutoYes is enabled.
func (i *Instance) TapEnter() {
	if !i.started || !i.AutoYes {
		return
	}
	if err := i.tmuxSession.TapEnter(); err != nil {
		log.ErrorLog.Printf("error tapping enter: %v", err)
	}
}

func (i *Instance) Attach() (chan struct{}, error) {
	if !i.started {
		return nil, fmt.Errorf("cannot attach instance that has not been started")
	}
	return i.tmuxSession.Attach()
}

func (i *Instance) SetPreviewSize(width, height int) error {
	if !i.started || i.Status == Paused {
		return fmt.Errorf("cannot set preview size for instance that has not been started or " +
			"is paused")
	}
	return i.tmuxSession.SetDetachedSize(width, height)
}

// GetGitWorktree returns the git worktree for the instance
func (i *Instance) GetGitWorktree() (*git.GitWorktree, error) {
	if !i.started {
		return nil, fmt.Errorf("cannot get git worktree for instance that has not been started")
	}
	// Never nil for a worktree-backed session, and an error rather than a nil
	// return for the other kind: every caller here goes on to act on the value,
	// and a nil worktree would panic on the first of them.
	if i.gitWorktree == nil {
		return nil, fmt.Errorf("session %q runs in the repository itself and has no worktree", i.Title)
	}
	return i.gitWorktree, nil
}

// GetWorktreePath returns the worktree path for the instance, or empty string if unavailable
func (i *Instance) GetWorktreePath() string {
	if i.gitWorktree == nil {
		return ""
	}
	return i.gitWorktree.GetWorktreePath()
}

// DevDir is the directory a development stack should run in for this session.
//
// A worktree-less session has no worktree but is still somewhere runnable: it
// works in the repository itself, which is exactly where its stack belongs.
// Returning "" for it would make the one session guaranteed to have a checked
// out, provisioned tree the only one that cannot run anything.
func (i *Instance) DevDir() string {
	if i.noWorktree {
		return i.Path
	}
	return i.GetWorktreePath()
}

func (i *Instance) Started() bool {
	return i.started
}

// SetTitle sets the title of the instance. Returns an error if the instance has started.
// We cant change the title once it's been used for a tmux session etc.
func (i *Instance) SetTitle(title string) error {
	if i.started {
		return fmt.Errorf("cannot change title of a started instance")
	}
	i.Title = title
	return nil
}

func (i *Instance) Paused() bool {
	return i.Status == Paused
}

// MarkGone parks a session whose tmux session has disappeared while it was
// running -- the agent exited, or the session was killed from outside -- exactly
// as a restart would have found it: Paused, worktree and branch left where they
// are, so resuming starts the agent again in the same place. Until this, only a
// restart noticed; in between the row read as Ready, or kept spinning, and the
// log filled with capture errors.
//
// Not reported as a finish: nothing finished, and ringing the bell for it would
// say so.
func (i *Instance) MarkGone() {
	if i.Status == Paused {
		return
	}
	log.WarningLog.Printf("tmux session for %q no longer exists; pausing instance so it can be resumed", i.Title)
	i.Status = Paused
	i.activity = ActivityIdle
	i.backgroundShells = 0
	i.doneAt = time.Time{}
}

// BranchCopied reports whether pausing managed to put the branch name on the
// clipboard, so the interface only says it did when it did.
func (i *Instance) BranchCopied() bool {
	return i.branchCopied
}

// copyToClipboard puts text on the system clipboard. The library covers X11 and
// Wayland; under WSL neither is usually installed and it fails, so Windows' own
// clip.exe is the fallback. The error used to be discarded while the interface
// announced "copied to your clipboard" regardless.
func copyToClipboard(text string) error {
	err := clipboard.WriteAll(text)
	if err == nil {
		return nil
	}
	if path, lookErr := exec.LookPath("clip.exe"); lookErr == nil {
		c := exec.Command(path)
		c.Stdin = strings.NewReader(text)
		if runErr := c.Run(); runErr == nil {
			return nil
		}
	}
	log.WarningLog.Printf("could not copy to the clipboard: %v", err)
	return err
}

// TmuxAlive returns true if the tmux session is alive. This is a sanity check before attaching.
func (i *Instance) TmuxAlive() bool {
	return i.tmuxSession.DoesSessionExist()
}

// Pause stops the tmux session and removes the worktree, preserving the branch
func (i *Instance) Pause() error {
	if !i.started {
		return fmt.Errorf("cannot pause instance that has not been started")
	}
	// Pausing commits the worktree's changes and removes it. There is no worktree
	// here, and the alternative -- committing and removing the user's own checkout
	// -- is emphatically not what pause means.
	if i.noWorktree {
		return fmt.Errorf("session %q runs in the repository itself, so there is no worktree to check out", i.Title)
	}
	if i.Status == Paused {
		return fmt.Errorf("instance is already paused")
	}

	var errs []error

	// If the worktree is orphaned (path or .git missing), git cannot operate
	// on it. Skip dirty check and Remove, prune any lingering metadata, then
	// transition to Paused so the user can recover via Resume.
	if valid, err := i.gitWorktree.IsValidWorktree(); err != nil {
		errs = append(errs, fmt.Errorf("failed to validate worktree: %w", err))
		log.ErrorLog.Print(err)
	} else if !valid {
		log.WarningLog.Printf("worktree at %s is orphaned; skipping dirty check and remove",
			i.gitWorktree.GetWorktreePath())
		if err := i.tmuxSession.DetachSafely(); err != nil {
			errs = append(errs, fmt.Errorf("failed to detach tmux session: %w", err))
			log.ErrorLog.Print(err)
		}
		// Drop any leftover directory so a future Resume's `git worktree add` won't conflict.
		if err := os.RemoveAll(i.gitWorktree.GetWorktreePath()); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove orphaned worktree directory: %w", err))
			log.ErrorLog.Print(err)
		}
		if err := i.gitWorktree.Prune(); err != nil {
			errs = append(errs, fmt.Errorf("failed to prune git worktrees: %w", err))
			log.ErrorLog.Print(err)
		}
		i.SetStatus(Paused)
		i.branchCopied = copyToClipboard(i.gitWorktree.GetBranchName()) == nil
		return i.combineErrors(errs)
	}

	// Check if there are any changes to commit
	if dirty, err := i.gitWorktree.IsDirty(); err != nil {
		errs = append(errs, fmt.Errorf("failed to check if worktree is dirty: %w", err))
		log.ErrorLog.Print(err)
	} else if dirty {
		// Commit changes locally (without pushing to GitHub)
		commitMsg := fmt.Sprintf("[adroit] update from '%s' on %s (paused)", i.Title, time.Now().Format(time.RFC822))
		if err := i.gitWorktree.CommitChanges(commitMsg); err != nil {
			errs = append(errs, fmt.Errorf("failed to commit changes: %w", err))
			log.ErrorLog.Print(err)
			// Return early if we can't commit changes to avoid corrupted state
			return i.combineErrors(errs)
		}
	}

	// Detach from tmux session instead of closing to preserve session output
	if err := i.tmuxSession.DetachSafely(); err != nil {
		errs = append(errs, fmt.Errorf("failed to detach tmux session: %w", err))
		log.ErrorLog.Print(err)
		// Continue with pause process even if detach fails
	}

	// Check if worktree exists before trying to remove it
	if _, err := os.Stat(i.gitWorktree.GetWorktreePath()); err == nil {
		// Remove worktree but keep branch
		if err := i.gitWorktree.Remove(); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove git worktree: %w", err))
			log.ErrorLog.Print(err)
			return i.combineErrors(errs)
		}

		// Only prune if remove was successful
		if err := i.gitWorktree.Prune(); err != nil {
			errs = append(errs, fmt.Errorf("failed to prune git worktrees: %w", err))
			log.ErrorLog.Print(err)
			return i.combineErrors(errs)
		}
	}

	i.SetStatus(Paused)
	i.branchCopied = copyToClipboard(i.gitWorktree.GetBranchName()) == nil

	if err := i.combineErrors(errs); err != nil {
		log.ErrorLog.Print(err)
		return err
	}
	return nil
}

// Resume recreates the worktree and restarts the tmux session
func (i *Instance) Resume() error {
	if !i.started {
		return fmt.Errorf("cannot resume instance that has not been started")
	}
	if i.Status != Paused {
		return fmt.Errorf("can only resume paused instances")
	}
	// A worktree-less session is never paused by the user -- Pause refuses it. It
	// reaches Paused only when Start could not restore its tmux session (a reboot,
	// a crash, `tmux kill-server`), so resuming it is exactly one thing: put a
	// session back in the repository. There is no worktree to validate, recreate
	// or check out, and no branch to guard, so none of the work below applies --
	// and refusing here left the row permanently dead, attachable only after a
	// kill and a re-create.
	if i.noWorktree {
		// Prefer the existing session if one is somehow there: Start refuses a
		// duplicate name, so reattaching is both correct and the only thing that
		// can work.
		if i.tmuxSession.DoesSessionExist() {
			if err := i.tmuxSession.Restore(); err != nil {
				log.ErrorLog.Print(err)
				return fmt.Errorf("failed to restore existing session: %w", err)
			}
			i.SetStatus(Running)
			return nil
		}
		if err := i.startTmux(i.Path); err != nil {
			log.ErrorLog.Print(err)
			return fmt.Errorf("failed to start new session: %w", err)
		}
		i.SetStatus(Running)
		return nil
	}

	// Check if branch is checked out
	if checked, err := i.gitWorktree.IsBranchCheckedOut(); err != nil {
		log.ErrorLog.Print(err)
		return fmt.Errorf("failed to check if branch is checked out: %w", err)
	} else if checked {
		return fmt.Errorf("cannot resume: branch is checked out, please switch to a different branch")
	}

	// Setup git worktree. Setup removes and re-adds the worktree from the branch, which
	// throws away anything uncommitted in it. After a normal Pause the directory is gone
	// and that is exactly what we want; but an instance paused because its tmux session
	// died still has its worktree — and the work in it — sitting on disk, so leave it be.
	if valid, err := i.gitWorktree.IsValidWorktree(); err != nil || !valid {
		if err != nil {
			log.WarningLog.Printf("could not validate worktree at %s, recreating it: %v",
				i.gitWorktree.GetWorktreePath(), err)
		}
		if err := i.gitWorktree.SetupForResume(); err != nil {
			log.ErrorLog.Print(err)
			return fmt.Errorf("failed to setup git worktree: %w", err)
		}
	}

	// Check if tmux session still exists from pause, otherwise create new one
	if i.tmuxSession.DoesSessionExist() {
		// Session exists, just restore PTY connection to it
		if err := i.tmuxSession.Restore(); err != nil {
			log.ErrorLog.Print(err)
			// If restore fails, fall back to creating new session
			if err := i.startTmux(i.gitWorktree.GetWorktreePath()); err != nil {
				log.ErrorLog.Print(err)
				return fmt.Errorf("failed to start new session: %w", err)
			}
		}
	} else {
		// Create new tmux session. A failure leaves the worktree and branch
		// exactly as they are: the Cleanup that used to run here is a kill's
		// teardown -- worktree remove -f and branch -D -- and the branch holds the
		// session's committed work. The session stays paused; r tries again.
		if err := i.startTmux(i.gitWorktree.GetWorktreePath()); err != nil {
			log.ErrorLog.Print(err)
			return fmt.Errorf("failed to start new session: %w", err)
		}
	}

	i.SetStatus(Running)
	return nil
}

// UpdateDiffStats updates the git diff statistics for this instance
func (i *Instance) UpdateDiffStats() error {
	if !i.started || i.gitWorktree == nil {
		i.diffStats = nil
		return nil
	}

	if i.Status == Paused {
		// Keep the previous diff stats if the instance is paused
		return nil
	}

	stats := i.gitWorktree.Diff()
	if stats.Error != nil {
		if strings.Contains(stats.Error.Error(), "base commit SHA not set") {
			// Worktree is not fully set up yet, not an error
			i.diffStats = nil
			return nil
		}
		return fmt.Errorf("failed to get diff stats: %w", stats.Error)
	}

	i.diffStats = stats
	return nil
}

// ComputeDiff runs the expensive git diff I/O and returns the result without
// mutating instance state. Safe to call from a background goroutine.
func (i *Instance) ComputeDiff() *git.DiffStats {
	// A worktree-less session has no isolated change set to report: whatever is
	// in the repository is the user's own working tree, not this session's work.
	if !i.started || i.Status == Paused || i.gitWorktree == nil {
		return nil
	}
	return i.gitWorktree.Diff()
}

// ComputeDiffNumstat runs a lightweight git diff --numstat and returns only the
// added/removed line counts (Content is left empty). Safe to call from a
// background goroutine. Use this for instances whose full diff content is not
// currently needed so we avoid keeping large diffs in memory.
func (i *Instance) ComputeDiffNumstat(stageUntracked bool) *git.DiffStats {
	// A worktree-less session has no isolated change set to report: whatever is
	// in the repository is the user's own working tree, not this session's work.
	if !i.started || i.Status == Paused || i.gitWorktree == nil {
		return nil
	}
	return i.gitWorktree.DiffNumstat(stageUntracked)
}

// SetDiffStats sets the diff statistics on the instance. Should be called from
// the main event loop to avoid data races with View.
//
// hasContent says whether stats carries the full diff text or only the counts,
// which is what lets DiffStale answer for the pane that is actually open.
func (i *Instance) SetDiffStats(stats *git.DiffStats, hasContent bool) {
	i.diffStats = stats
	i.diffStatsAt = time.Now()
	i.diffStatsHasContent = hasContent && stats != nil
}

// DiffStatsAt is when the current diff stats were computed. Exact identity for
// anything that caches a rendering of them: the timestamp only moves when the
// stats were actually recomputed.
func (i *Instance) DiffStatsAt() time.Time {
	return i.diffStatsAt
}

// UntrackedStale reports whether the next diff should pay for the untracked-file
// walk.
func (i *Instance) UntrackedStale(maxAge time.Duration) bool {
	return i.untrackedAt.IsZero() || time.Since(i.untrackedAt) > maxAge
}

// MarkUntrackedStaged records that a diff has just folded untracked files in.
func (i *Instance) MarkUntrackedStaged() {
	i.untrackedAt = time.Now()
}

// DiffStale reports whether the diff needs recomputing: older than maxAge, or
// wanted with content it was not computed with.
//
// The other half of the answer is whether the session's pane changed, which the
// tick learns from the same capture it takes anyway -- see the call site. Files
// change when the agent writes, and the agent writing redraws its pane, so that
// covers the common case and maxAge covers the one it misses: a background shell
// writing to disk with nothing on screen to show for it.
func (i *Instance) DiffStale(maxAge time.Duration, wantContent bool) bool {
	if i.diffStatsAt.IsZero() {
		return true
	}
	if wantContent && !i.diffStatsHasContent {
		return true
	}
	return time.Since(i.diffStatsAt) > maxAge
}

// GetDiffStats returns the current git diff statistics
func (i *Instance) GetDiffStats() *git.DiffStats {
	return i.diffStats
}

// SetCIStatus sets the CI verdict for this instance's branch. Should be called
// from the main event loop to avoid data races with View.
func (i *Instance) SetCIStatus(status ci.Status) {
	i.ciStatus = status
}

// GetCIStatus returns the last known CI verdict for this instance's branch.
func (i *Instance) GetCIStatus() ci.Status {
	return i.ciStatus
}

// SetUpstreamStatus records how this instance's branch stands against its
// remote counterpart. Should be called from the main event loop to avoid data
// races with View.
func (i *Instance) SetUpstreamStatus(status upstream.Status) {
	i.upstreamStatus = status
}

// GetUpstreamStatus returns the last known standing against the remote.
func (i *Instance) GetUpstreamStatus() upstream.Status {
	return i.upstreamStatus
}

// SetActivity records what the agent is doing, and notes the moment it stops.
//
// The transition is the load-bearing half. A session goes idle once per turn, and
// stamping when that happened is what lets the list say "this just finished"
// rather than only "this is idle".
func (i *Instance) SetActivity(activity Activity, backgroundShells int) {
	if activity != ActivityIdle {
		i.lastActiveAt = time.Now()
	}
	switch {
	case activity == ActivityIdle && i.activity != ActivityIdle:
		i.doneAt = time.Now()
	case activity != ActivityIdle:
		// Busy again, so any earlier finish is stale: a session that went idle and
		// was then given more work must not still be announcing the first turn.
		i.doneAt = time.Time{}
	}
	i.activity = activity
	i.backgroundShells = backgroundShells
}

// GetActivity returns the last known agent activity for this session.
func (i *Instance) GetActivity() Activity {
	return i.activity
}

// GetBackgroundShells returns how many shells the agent has left running.
func (i *Instance) GetBackgroundShells() int {
	return i.backgroundShells
}

// DoneAt is when this session last went idle, or the zero time if it is busy or
// the finish has already been acknowledged.
func (i *Instance) DoneAt() time.Time {
	return i.doneAt
}

// Acknowledge clears the just-finished mark: the cursor has moved onto the row,
// or you have attached to it, so it has nothing left to announce.
func (i *Instance) Acknowledge() {
	i.doneAt = time.Time{}
}

// SetCurrentBranch records the branch the worktree has checked out. Should be
// called from the main event loop to avoid data races with View.
func (i *Instance) SetCurrentBranch(branch string) {
	i.currentBranch = branch
	i.currentBranchAt = time.Now()
}

// BranchStale reports whether the checked-out branch is worth re-reading.
func (i *Instance) BranchStale(maxAge time.Duration) bool {
	return i.currentBranchAt.IsZero() || time.Since(i.currentBranchAt) > maxAge
}

// ResolveCurrentBranch reads the worktree's HEAD. Returns "" when there is no
// answer — a detached HEAD, a worktree that has gone away, or an instance that
// never started — which the caller reads as "keep using the recorded name".
//
// Safe to call off the main thread: it touches no instance state.
func (i *Instance) ResolveCurrentBranch() string {
	if !i.started || i.gitWorktree == nil {
		return ""
	}
	branch, err := git.CurrentBranch(i.gitWorktree.GetWorktreePath())
	if err != nil {
		return ""
	}
	return branch
}

// DisplayBranch is the branch name to show for this instance: the one the
// worktree actually has checked out, falling back to the name the session was
// created with.
//
// The two differ after a rename or a checkout inside the worktree, and the
// recorded name is then a branch that may not exist any more — so the row was
// labelling work with a name nothing else in the repo, or on GitHub, would
// recognise. This is display only: Branch stays the name every operation uses,
// because that is what the session owns and is responsible for cleaning up.
func (i *Instance) DisplayBranch() string {
	if i.currentBranch != "" {
		return i.currentBranch
	}
	return i.Branch
}

// SendPrompt sends a prompt to the tmux session
func (i *Instance) SendPrompt(prompt string) error {
	if !i.started {
		return fmt.Errorf("instance not started")
	}
	if i.tmuxSession == nil {
		return fmt.Errorf("tmux session not initialized")
	}
	// A newline typed into the agent is enter, which would send the first line
	// alone. Bracketed paste delivers the whole text as one input; tmux passes
	// the markers through to a program that has asked for them, as Claude Code
	// does.
	if strings.Contains(prompt, "\n") {
		prompt = "\x1b[200~" + prompt + "\x1b[201~"
	}
	if err := i.tmuxSession.SendKeys(prompt); err != nil {
		return fmt.Errorf("error sending keys to tmux session: %w", err)
	}

	// Brief pause to prevent carriage return from being interpreted as newline
	time.Sleep(100 * time.Millisecond)
	if err := i.tmuxSession.TapEnter(); err != nil {
		return fmt.Errorf("error tapping enter: %w", err)
	}

	return nil
}

// PreviewFullHistory captures the entire tmux pane output including full scrollback history
func (i *Instance) PreviewFullHistory() (string, error) {
	if !i.started || i.Status == Paused {
		return "", nil
	}
	return i.tmuxSession.CapturePaneContentWithOptions("-", "-")
}

// SetTmuxSession sets the tmux session for testing purposes
func (i *Instance) SetTmuxSession(session *tmux.TmuxSession) {
	i.tmuxSession = session
}

// SendKeys sends keys to the tmux session
func (i *Instance) SendKeys(keys string) error {
	if !i.started || i.Status == Paused {
		return fmt.Errorf("cannot send keys to instance that has not been started or is paused")
	}
	return i.tmuxSession.SendKeys(keys)
}
