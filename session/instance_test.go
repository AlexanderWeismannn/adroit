package session

import (
	"fmt"
	"github.com/AlexanderWeismannn/adroit/cmd/cmd_test"
	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session/git"
	"github.com/AlexanderWeismannn/adroit/session/resume"
	"github.com/AlexanderWeismannn/adroit/session/tmux"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	log.Initialize(false)
	defer log.Close()
	os.Exit(m.Run())
}

// nullPtyFactory hands back a throwaway file instead of a real PTY.
type nullPtyFactory struct {
	t     *testing.T
	calls int
}

func (p *nullPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	p.calls++
	return os.OpenFile(filepath.Join(p.t.TempDir(), "pty"), os.O_CREATE|os.O_RDWR, 0644)
}

func (p *nullPtyFactory) Close() {}

// When the tmux server dies between runs, every session goes with it while the worktree
// and branch survive on disk. Restoring such an instance must park it as Paused so the
// user can resume it. Returning an error instead is not an option: LoadInstances aborts on
// the first failure, so one dead session would hide every other instance.
// See https://github.com/smtg-ai/claude-squad/issues/216.
func TestStartPausesInstanceWhenTmuxSessionNoLongerExists(t *testing.T) {
	ptyFactory := &nullPtyFactory{t: t}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") {
				return fmt.Errorf("can't find session")
			}
			return nil
		},
	}

	instance, err := NewInstance(InstanceOptions{Title: "revived", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	instance.SetTmuxSession(tmux.NewTmuxSessionWithDeps("revived", "claude", ptyFactory, cmdExec))

	require.NoError(t, instance.Start(false), "a dead tmux session is recoverable, not a startup failure")
	require.Equal(t, Paused, instance.Status)
	require.True(t, instance.Started())
	require.Zero(t, ptyFactory.calls, "should not attach to a session that does not exist")
}

// The happy path is unchanged: an instance whose session survived comes back Running.
func TestStartRestoresInstanceWhenTmuxSessionSurvives(t *testing.T) {
	ptyFactory := &nullPtyFactory{t: t}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error { return nil },
	}

	instance, err := NewInstance(InstanceOptions{Title: "alive", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	instance.SetTmuxSession(tmux.NewTmuxSessionWithDeps("alive", "claude", ptyFactory, cmdExec))

	require.NoError(t, instance.Start(false))
	require.Equal(t, Running, instance.Status)
	require.Equal(t, 1, ptyFactory.calls)
}

// The row used to be labelled with the branch the session was created with, so a
// rename or a checkout inside the worktree left it naming something the repo no
// longer had. Branch itself must not move: every operation the session owns —
// push, checkout, the branch it deletes on kill — keys on it.
func TestDisplayBranchPrefersTheResolvedBranch(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "session", Path: ".", Program: "echo"})
	if err != nil {
		t.Fatalf("NewInstance: %v", err)
	}
	inst.Branch = "task-recorded"

	if got := inst.DisplayBranch(); got != "task-recorded" {
		t.Errorf("with nothing resolved, DisplayBranch() = %q, want the recorded name", got)
	}

	inst.SetCurrentBranch("TASK-5636-Vis-6")
	if got := inst.DisplayBranch(); got != "TASK-5636-Vis-6" {
		t.Errorf("DisplayBranch() = %q, want the resolved branch", got)
	}
	if inst.Branch != "task-recorded" {
		t.Errorf("Branch = %q, want it left alone — operations key on it", inst.Branch)
	}

	// "" is what an unresolvable HEAD reports (detached, worktree gone, never
	// started). It must fall back rather than blank the row's identity.
	inst.SetCurrentBranch("")
	if got := inst.DisplayBranch(); got != "task-recorded" {
		t.Errorf("after an unresolvable HEAD, DisplayBranch() = %q, want the recorded name", got)
	}
}

// An instance that never started has no worktree to read a HEAD from.
func TestResolveCurrentBranchOnAnUnstartedInstance(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "session", Path: ".", Program: "echo"})
	if err != nil {
		t.Fatalf("NewInstance: %v", err)
	}
	if got := inst.ResolveCurrentBranch(); got != "" {
		t.Errorf("ResolveCurrentBranch() = %q, want \"\"", got)
	}
}

// A session that runs in the repository itself must never acquire a worktree.
// Cleanup() force-removes the worktree and deletes its branch, so a GitWorktree
// rooted at the user's own checkout would make `D` destroy it — this is the one
// property the whole feature rests on.
func TestNoWorktreeSessionNeverBuildsAWorktree(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "scratch", Path: ".", Program: "echo", NoWorktree: true})
	if err != nil {
		t.Fatalf("NewInstance: %v", err)
	}
	if !inst.NoWorktree() {
		t.Fatal("NoWorktree() should report the option it was constructed with")
	}
	if _, err := inst.GetGitWorktree(); err == nil {
		t.Error("GetGitWorktree should refuse rather than hand back a nil worktree")
	}

	// Kill is nil-safe, so it must not report an error for a session that never
	// started and has nothing to clean up.
	if err := inst.Kill(); err != nil {
		t.Errorf("Kill on an unstarted worktree-less instance: %v", err)
	}
}

// The flag has to survive a reload, and it is stored rather than inferred from
// an empty worktree: a rebuilt GitWorktree would point every worktree operation
// at a path derived from the repository.
func TestNoWorktreeSurvivesSerialization(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "scratch", Path: ".", Program: "echo", NoWorktree: true})
	if err != nil {
		t.Fatalf("NewInstance: %v", err)
	}

	data := inst.ToInstanceData()
	if !data.NoWorktree {
		t.Fatal("ToInstanceData dropped the flag")
	}

	restored, err := FromInstanceData(data)
	if err != nil {
		t.Fatalf("FromInstanceData: %v", err)
	}
	if !restored.NoWorktree() {
		t.Error("the flag did not survive the round trip")
	}
	if restored.gitWorktree != nil {
		t.Error("a worktree was rebuilt for a session that must never have one")
	}
	if got := restored.ComputeDiffNumstat(true); got != nil {
		t.Errorf("ComputeDiffNumstat = %v, want nil — there is no isolated change set", got)
	}
}

// recordingPtyFactory records the commands it is asked to start, and reports the
// tmux session as existing once new-session has run.
type recordingPtyFactory struct {
	t       *testing.T
	cmds    []string
	created bool
}

func (p *recordingPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	p.cmds = append(p.cmds, cmd.String())
	if strings.Contains(cmd.String(), "new-session") {
		p.created = true
	}
	return os.OpenFile(filepath.Join(p.t.TempDir(), "pty"), os.O_CREATE|os.O_RDWR, 0644)
}

func (p *recordingPtyFactory) Close() {}

// Pause commits the worktree and removes it, so it refuses a worktree-less
// session: there is nothing of the session's own to pause, and the alternative
// is committing and removing the user's own checkout.
//
// Resume is the asymmetric half. Such a session never reaches Paused by the
// user's hand -- it gets there only when Start could not restore its tmux
// session (a reboot, a crash, `tmux kill-server`) -- and resuming it is exactly
// one thing: put a session back in the repository. Refusing there left the row
// permanently dead: not attachable, not resumable, only killable.
func TestNoWorktreeSessionRefusesPauseButResumes(t *testing.T) {
	newPausedRepoSession := func(t *testing.T, repo string) (*Instance, *recordingPtyFactory) {
		ptyFactory := &recordingPtyFactory{t: t}
		cmdExec := cmd_test.MockCmdExec{
			RunFunc: func(cmd *exec.Cmd) error {
				if strings.Contains(cmd.String(), "has-session") && !ptyFactory.created {
					return fmt.Errorf("can't find session")
				}
				return nil
			},
		}

		inst, err := NewInstance(InstanceOptions{
			Title: "scratch", Path: repo, Program: "echo", NoWorktree: true,
		})
		require.NoError(t, err)
		inst.SetTmuxSession(tmux.NewTmuxSessionWithDeps("scratch", "echo", ptyFactory, cmdExec))
		inst.started = true
		inst.SetStatus(Paused)
		return inst, ptyFactory
	}

	t.Run("pause is refused", func(t *testing.T) {
		inst, err := NewInstance(InstanceOptions{
			Title: "scratch", Path: ".", Program: "echo", NoWorktree: true,
		})
		require.NoError(t, err)
		inst.started = true

		require.Error(t, inst.Pause(), "Pause should refuse a worktree-less session")
	})

	t.Run("resume starts a session in the repository itself", func(t *testing.T) {
		repo := t.TempDir()
		inst, ptyFactory := newPausedRepoSession(t, repo)

		require.NoError(t, inst.Resume())
		require.Equal(t, Running, inst.Status)
		require.NotEmpty(t, ptyFactory.cmds)
		require.Contains(t, ptyFactory.cmds[0], "new-session")
		require.Contains(t, ptyFactory.cmds[0], repo,
			"the session runs in the repository, not in a worktree")
	})

	t.Run("resume reattaches when the session is somehow still there", func(t *testing.T) {
		inst, ptyFactory := newPausedRepoSession(t, t.TempDir())
		// Start refuses a duplicate name, so reattaching is the only thing that works.
		ptyFactory.created = true

		require.NoError(t, inst.Resume())
		require.Equal(t, Running, inst.Status)
		require.Len(t, ptyFactory.cmds, 1)
		require.Contains(t, ptyFactory.cmds[0], "attach-session")
	})
}

// The just-finished mark has to be stamped on the transition into idle and
// nowhere else. The metadata tick calls SetActivity twice a second, so a stamp
// taken every time the session is merely idle would keep resetting the window and
// the mark would never expire.
func TestSetActivityStampsOnlyTheTransitionToIdle(t *testing.T) {
	inst := &Instance{}

	// Idle from the start is a session that has never worked, and has nothing to
	// announce.
	inst.SetActivity(ActivityIdle, 0)
	require.True(t, inst.DoneAt().IsZero())

	inst.SetActivity(ActivityWorking, 0)
	require.True(t, inst.DoneAt().IsZero())

	inst.SetActivity(ActivityIdle, 0)
	stamped := inst.DoneAt()
	require.False(t, stamped.IsZero())

	// Idle again on the next tick is the same finish, not a new one.
	inst.SetActivity(ActivityIdle, 0)
	require.Equal(t, stamped, inst.DoneAt())

	// Busy again clears it: a session given more work must not still be
	// announcing the end of the last turn.
	inst.SetActivity(ActivityShell, 2)
	require.True(t, inst.DoneAt().IsZero())
	require.Equal(t, 2, inst.GetBackgroundShells())
	require.Equal(t, ActivityShell, inst.GetActivity())

	inst.SetActivity(ActivityIdle, 0)
	require.False(t, inst.DoneAt().IsZero())
	inst.Acknowledge()
	require.True(t, inst.DoneAt().IsZero())
}

// The row shows how long a session has been quiet, which needs a stamp that
// outlives the acknowledgement doneAt does not: a finish you have already
// looked at is still a session that has done nothing for an hour.
func TestLastActiveOutlivesTheFinishMark(t *testing.T) {
	inst := &Instance{Title: "a-session", Path: t.TempDir(), Program: "echo", noWorktree: true}
	require.True(t, inst.LastActiveAt().IsZero(), "a session that has done nothing claims no age")

	inst.SetActivity(ActivityWorking, 0)
	working := inst.LastActiveAt()
	require.False(t, working.IsZero())

	// Going idle is not activity, so the stamp stays where the work left it.
	inst.SetActivity(ActivityIdle, 0)
	inst.Acknowledge()
	require.Equal(t, working, inst.LastActiveAt())

	// A program whose interface cannot be read reports only through its status,
	// and a pane that moved is the one piece of evidence it is doing anything.
	inst.SetStatus(Running)
	require.True(t, inst.LastActiveAt().After(working))
	running := inst.LastActiveAt()
	inst.SetStatus(Ready)
	require.Equal(t, running, inst.LastActiveAt())

	// And it survives a restart, or every row would read as ageless for as long
	// as it took each session to do something again.
	restored, err := FromInstanceData(inst.ToInstanceData())
	require.NoError(t, err)
	require.Equal(t, running, restored.LastActiveAt())
}

// The diff is the most expensive thing the interface does -- two git
// subprocesses, measured at 20ms per session -- so what counts as stale is
// load-bearing rather than a detail.
func TestDiffStaleness(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "a", Path: ".", Program: "echo"})
	require.NoError(t, err)

	require.True(t, inst.DiffStale(time.Minute, false), "never computed")

	inst.SetDiffStats(&git.DiffStats{Added: 1}, false)
	require.False(t, inst.DiffStale(time.Minute, false))
	// Counts are in hand but the diff pane wants the text, which was not loaded.
	require.True(t, inst.DiffStale(time.Minute, true))

	inst.SetDiffStats(&git.DiffStats{Added: 1, Content: "diff"}, true)
	require.False(t, inst.DiffStale(time.Minute, true))
	// A zero floor is what the caller passes to mean "always".
	require.True(t, inst.DiffStale(0, false))
}

// A recompute is what moves the stamp, which is how the diff pane knows it can
// keep the rendering it already built.
func TestDiffStatsAtMovesOnlyOnRecompute(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "a", Path: ".", Program: "echo"})
	require.NoError(t, err)
	require.True(t, inst.DiffStatsAt().IsZero())

	inst.SetDiffStats(&git.DiffStats{Added: 1}, false)
	first := inst.DiffStatsAt()
	require.False(t, first.IsZero())

	inst.SetDiffStats(&git.DiffStats{Added: 2}, false)
	require.True(t, inst.DiffStatsAt().After(first) || inst.DiffStatsAt().Equal(first))
}

func TestBranchStaleness(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "a", Path: ".", Program: "echo"})
	require.NoError(t, err)

	require.True(t, inst.BranchStale(time.Minute), "never resolved")
	inst.SetCurrentBranch("TASK-1")
	require.False(t, inst.BranchStale(time.Minute))
	require.True(t, inst.BranchStale(0))
}

// The whole point of recording a session id: when the tmux server dies -- a
// reboot, a crash, or the WSL VM stopping because the last terminal window was
// closed -- resuming used to start an empty Claude in the pane, and the
// conversation was simply gone. Nothing about the row looked different, which
// is why it went unnoticed for so long.
func TestLaunchCommandNamesANewConversationAndResumesIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	workdir := t.TempDir()

	instance := &Instance{Title: "s", Path: workdir, Program: "claude"}

	first := instance.launchCommandFor(workdir)
	require.NotEmpty(t, instance.SessionID, "the conversation is named up front, not discovered later")
	require.Equal(t, "claude --session-id "+instance.SessionID, first)
	require.False(t, instance.ResumedConversation())

	// The agent has now said something, so the transcript exists.
	writeTranscriptFor(t, workdir, instance.SessionID)

	second := instance.launchCommandFor(workdir)
	require.Equal(t, "claude --resume "+instance.SessionID, second)
	require.True(t, instance.ResumedConversation())
}

// `claude --session-id` refuses an id it has already used, and a transcript
// that exists but is empty -- started, never spoken to -- counts as used.
// Reusing it would fail in the pane, which is worse than losing an empty
// conversation.
func TestLaunchCommandMintsAFreshIDWhenTheTranscriptIsEmpty(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	workdir := t.TempDir()

	instance := &Instance{Title: "s", Path: workdir, Program: "claude"}
	instance.launchCommandFor(workdir)
	burned := instance.SessionID

	dir, err := resume.ProjectDir(workdir)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, burned+".jsonl"), nil, 0o644))

	next := instance.launchCommandFor(workdir)
	require.NotEqual(t, burned, instance.SessionID)
	require.Equal(t, "claude --session-id "+instance.SessionID, next)
	require.False(t, instance.ResumedConversation())
}

// A program the caller has already aimed at a conversation -- the restore
// picker builds `--resume <id>` -- must be left alone. Appending our own flag
// produces a command Claude refuses, so the pane shows a usage error instead of
// an agent.
func TestLaunchCommandLeavesAnAimedProgramAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	workdir := t.TempDir()

	restored := &Instance{Title: "s", Path: workdir, Program: "claude --resume old-id"}
	require.Equal(t, "claude --resume old-id", restored.launchCommandFor(workdir))
	require.Empty(t, restored.SessionID)

	other := &Instance{Title: "s", Path: workdir, Program: "aider"}
	require.Equal(t, "aider", other.launchCommandFor(workdir))
	require.Empty(t, other.SessionID, "only Claude takes a session id")
}

// Sessions created before Adroit named its own conversations have one on disk
// and nothing pointing at it. Adopting is worth doing, but only when the answer
// is not a guess.
func TestLaunchCommandAdoptsTheOnlyConversationInTheDirectory(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	workdir := t.TempDir()
	writeTranscriptFor(t, workdir, "pre-existing")

	instance := &Instance{Title: "s", Path: workdir, Program: "claude"}
	require.Equal(t, "claude --resume pre-existing", instance.launchCommandFor(workdir))
	require.Equal(t, "pre-existing", instance.SessionID)
	require.True(t, instance.ResumedConversation())

	// A second conversation in the same folder -- two worktree-less sessions on
	// one repository -- makes the answer a guess, and the wrong conversation is
	// worse than a fresh one.
	writeTranscriptFor(t, workdir, "another")
	fresh := &Instance{Title: "t", Path: workdir, Program: "claude"}
	command := fresh.launchCommandFor(workdir)
	require.Contains(t, command, "--session-id")
	require.False(t, fresh.ResumedConversation())
}

// writeTranscriptFor puts a conversation where Claude Code would have left one.
func writeTranscriptFor(t *testing.T, workdir, sessionID string) {
	t.Helper()
	dir, err := resume.ProjectDir(workdir)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	record := fmt.Sprintf(
		`{"type":"user","sessionId":%q,"cwd":%q,"message":{"role":"user","content":"hello"}}`+"\n",
		sessionID, workdir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(record), 0o644))
}

// failingPtyFactory refuses every PTY, as a box out of /dev/pts entries does.
type failingPtyFactory struct{}

func (failingPtyFactory) Start(*exec.Cmd) (*os.File, error) { return nil, fmt.Errorf("out of ptys") }
func (failingPtyFactory) Close()                            {}

// The session is alive but its PTY cannot be opened. That used to fail Start,
// which failed LoadInstances, which exited Adroit before it drew anything --
// and every relaunch hit the same session and failed the same way, so Adroit
// could not be started at all until tmux was killed. Parked, it is one row to
// resume later.
func TestStartPausesInstanceWhenItsPtyCannotBeOpened(t *testing.T) {
	cmdExec := cmd_test.MockCmdExec{RunFunc: func(*exec.Cmd) error { return nil }} // has-session succeeds
	instance, err := NewInstance(InstanceOptions{Title: "no-pty", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	instance.SetTmuxSession(tmux.NewTmuxSessionWithDeps("no-pty", "claude", failingPtyFactory{}, cmdExec))

	require.NoError(t, instance.Start(false), "an unopenable PTY must not stop Adroit from starting")
	require.Equal(t, Paused, instance.Status)
}

// A resume whose tmux failed to start used to "clean up" with the teardown a
// kill uses: worktree remove -f and branch -D. The branch is the session's work
// -- everything the agent committed, and everything a checkout committed for it
// -- and a tmux start that merely timed out under load deleted all of it. The
// failure must leave the session paused with its branch and worktree intact.
func TestAFailedResumeKeepsTheBranchAndTheWorktree(t *testing.T) {
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	repo := t.TempDir()
	run(repo, "init", "-q", "-b", "main")
	run(repo, "commit", "-q", "--allow-empty", "-m", "base")
	base := run(repo, "rev-parse", "HEAD")
	worktree := filepath.Join(t.TempDir(), "wt")
	run(repo, "worktree", "add", "-q", "-b", "agent-work", worktree)
	run(worktree, "commit", "-q", "--allow-empty", "-m", "the agent's work")

	instance, err := FromInstanceData(InstanceData{
		Title: "resume-fails", Path: repo, Program: "claude", Status: Paused,
		Worktree: GitWorktreeData{RepoPath: repo, WorktreePath: worktree, SessionName: "resume-fails",
			BranchName: "agent-work", BaseCommitSHA: base, BaseBranch: "main"},
	})
	require.NoError(t, err)
	cmdExec := cmd_test.MockCmdExec{RunFunc: func(cmd *exec.Cmd) error {
		if strings.Contains(cmd.String(), "has-session") {
			return fmt.Errorf("can't find session")
		}
		return nil
	}}
	instance.SetTmuxSession(tmux.NewTmuxSessionWithDeps("resume-fails", "claude", failingPtyFactory{}, cmdExec))

	require.Error(t, instance.Resume())
	require.Equal(t, Paused, instance.Status)
	require.Equal(t, "the agent's work", run(repo, "log", "-1", "--format=%s", "agent-work"), "the branch must survive")
	require.DirExists(t, worktree, "the worktree must survive")
}

// The tmux command line is visible to every process on the machine, so a key
// must never be in it: the launch names the profile and lets agent-run fetch it.
func TestLaunchWithKeysNamesTheProfileNotTheKey(t *testing.T) {
	cfg := &config.Config{Profiles: []config.Profile{
		{Name: "codex", Program: "codex --full-auto", Keys: []string{"OPENAI_API_KEY"}},
		{Name: "claude", Program: "claude"},
	}}
	got := withAgentKeys("codex --full-auto 'it''s'", "codex --full-auto", cfg)
	require.Contains(t, got, " agent-run --profile 'codex' -- ")
	require.NotContains(t, got, "sk-")

	require.Equal(t, "claude", withAgentKeys("claude", "claude", cfg), "a profile with no keys launches as before")
	require.Equal(t, "aider", withAgentKeys("aider", "aider", cfg), "no profile, no wrapper")
}
