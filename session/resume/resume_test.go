package resume

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The slug is not ours to choose: it is how Claude Code names the project
// directory it looks in, so getting it wrong means Find silently reports that a
// session never had a conversation. These are real directory names taken from a
// machine running Adroit.
func TestSlugMatchesClaudesProjectDirectoryNames(t *testing.T) {
	assert.Equal(t,
		"-home-jane--adroit-worktrees-TASK-5866-BILLING-18d3f758ebc0c3db",
		Slug("/home/jane/.adroit/worktrees/TASK-5866-BILLING_18d3f758ebc0c3db"),
		"the dot of .adroit and the underscore before the suffix both become dashes")
	assert.Equal(t, "-home-jane-myapp", Slug("/home/jane/myapp"))
}

// writeTranscript puts a conversation where Find will look for one.
func writeTranscript(t *testing.T, workdir, sessionID string, lines ...string) string {
	t.Helper()
	dir, err := ProjectDir(workdir)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, sessionID+".jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return path
}

func TestFindReadsTheNewestConversation(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	workdir := "/home/u/.adroit/worktrees/TASK-1_abc"

	writeTranscript(t, workdir, "old-session",
		`{"type":"user","sessionId":"old-session","cwd":"/home/u/.adroit/worktrees/TASK-1_abc","message":{"role":"user","content":"the older one"}}`)
	newest := writeTranscript(t, workdir, "new-session",
		`{"type":"mode","sessionId":"new-session"}`,
		`{"type":"user","sessionId":"new-session","cwd":"/home/u/.adroit/worktrees/TASK-1_abc","gitBranch":"TASK-1","message":{"role":"user","content":"<local-command-caveat>ignore me</local-command-caveat>"}}`,
		`{"type":"user","sessionId":"new-session","message":{"role":"user","content":[{"type":"text","text":"fix   the flaky test"}]}}`)

	// Both files are written in the same instant on a fast filesystem, so the
	// ordering under test is made explicit rather than left to the clock.
	require.NoError(t, os.Chtimes(newest, timeLater(), timeLater()))

	transcript, ok := Find(workdir)
	require.True(t, ok)
	assert.Equal(t, "new-session", transcript.SessionID)
	assert.Equal(t, "/home/u/.adroit/worktrees/TASK-1_abc", transcript.CWD)
	assert.Equal(t, "TASK-1", transcript.Branch)
	// The caveat Claude prepends to local command output is machinery, not
	// something the user said, so the row would have been labelled with a chunk
	// of XML nobody wrote.
	assert.Equal(t, "fix the flaky test", transcript.Summary)
}

func TestFindReportsNoConversation(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, ok := Find("/home/u/.adroit/worktrees/never-ran_abc")
	assert.False(t, ok, "a directory that never ran Claude has nothing to restore")
}

// A restored session always gets a NEW worktree path, and Claude resolves
// --resume against the project directory of the current one only. Without the
// copy the id is unresolvable and the session starts with no history at all.
func TestInstallMakesTheConversationResumableFromANewDirectory(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	oldWorkdir := "/home/u/.adroit/worktrees/TASK-1_aaa"
	newWorkdir := "/home/u/.adroit/worktrees/TASK-1_bbb"
	writeTranscript(t, oldWorkdir, "conversation",
		`{"type":"user","sessionId":"conversation","cwd":"/home/u/.adroit/worktrees/TASK-1_aaa","message":{"role":"user","content":"carry on"}}`)

	transcript, ok := Find(oldWorkdir)
	require.True(t, ok)

	dest, err := Install(transcript, newWorkdir)
	require.NoError(t, err)

	newDir, err := ProjectDir(newWorkdir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(newDir, "conversation.jsonl"), dest)

	copied, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Contains(t, string(copied), "carry on")

	// The original is a copy source, never a move source: the conversation must
	// still be there if the restore is abandoned or repeated.
	_, err = os.Stat(transcript.Path)
	assert.NoError(t, err)

	// Nothing is left behind from the atomic write.
	entries, err := os.ReadDir(newDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

// Installing over a conversation already in place would truncate one that has
// since moved on -- the restored session writes to this very file.
func TestInstallLeavesAnExistingConversationAlone(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	workdir := "/home/u/.adroit/worktrees/TASK-1_aaa"
	source := writeTranscript(t, workdir, "conversation", `{"type":"user","message":{"role":"user","content":"original"}}`)

	newWorkdir := "/home/u/.adroit/worktrees/TASK-1_bbb"
	existing := writeTranscript(t, newWorkdir, "conversation", `{"type":"user","message":{"role":"user","content":"kept going"}}`)

	_, err := Install(Transcript{SessionID: "conversation", Path: source}, newWorkdir)
	require.NoError(t, err)

	content, err := os.ReadFile(existing)
	require.NoError(t, err)
	assert.Contains(t, string(content), "kept going")
}

// The session id is Claude's; handing it to another agent produces a program
// that refuses to start, which would turn a restore into a dead row.
func TestCommandOnlyResumesClaude(t *testing.T) {
	assert.Equal(t, "claude --resume abc", Command("claude", "abc"))
	assert.Equal(t, "/usr/local/bin/claude --model opus --resume abc",
		Command("/usr/local/bin/claude --model opus", "abc"))
	assert.Equal(t, "aider", Command("aider", "abc"))
	assert.Equal(t, "claude", Command("claude", ""))
}

// timeLater is a fixed point comfortably after any file this test just wrote.
func timeLater() time.Time { return time.Now().Add(time.Hour) }

// Sole is what lets a session created before Adroit named its own conversations
// pick one back up after a crash. Every way it can be wrong is silent, and the
// expensive one is not "no conversation" but "somebody else's conversation":
// Claude files transcripts by working directory, so the sessions that run
// without a worktree of their own all write into one folder.
func TestSoleRefusesToGuessBetweenConversations(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	workdir := "/home/u/myapp"
	record := `{"type":"user","sessionId":"%s","cwd":"` + workdir + `","message":{"role":"user","content":"hi"}}`

	writeTranscript(t, workdir, "aaa", fmt.Sprintf(record, "aaa"))
	transcript, ok := Sole(workdir)
	require.True(t, ok, "one conversation in the directory is the case this exists for")
	assert.Equal(t, "aaa", transcript.SessionID)

	writeTranscript(t, workdir, "bbb", fmt.Sprintf(record, "bbb"))
	_, ok = Sole(workdir)
	assert.False(t, ok, "two conversations share the directory, so neither belongs to the asking session")
}

func TestSoleIgnoresADirectoryWithNothingInIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	_, ok := Sole("/home/u/never-ran-claude")
	assert.False(t, ok)
}

// An empty transcript is Claude having been started and never spoken to.
// Resuming it fails in the pane, so it must not read as a conversation.
func TestSoleAndExistsIgnoreAnEmptyTranscript(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	workdir := "/home/u/.adroit/worktrees/TASK-1_abc"
	dir, err := ProjectDir(workdir)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "empty.jsonl"), nil, 0o644))

	_, ok := Sole(workdir)
	assert.False(t, ok)
	assert.False(t, Exists(workdir, "empty"))
}

func TestExistsFindsTheTranscriptItNamed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	workdir := "/home/u/.adroit/worktrees/TASK-1_abc"
	writeTranscript(t, workdir, "abc-123", `{"type":"user","sessionId":"abc-123"}`)

	assert.True(t, Exists(workdir, "abc-123"))
	assert.False(t, Exists(workdir, "some-other-id"))
	assert.False(t, Exists(workdir, ""), "no id is not a conversation")
}

// Appending our own flag to a command that already carries one produces
// `claude --resume A --session-id B`, which Claude refuses -- so the pane shows
// a usage error instead of an agent. The restore picker builds `--resume`, and a
// user is free to put one in their configured program.
func TestSessionFlagsAreNotDoubledUp(t *testing.T) {
	assert.True(t, CarriesSessionFlag("claude --resume abc"))
	assert.True(t, CarriesSessionFlag("claude --resume=abc"))
	assert.True(t, CarriesSessionFlag("claude --session-id abc"))
	assert.True(t, CarriesSessionFlag("claude -c"))
	assert.False(t, CarriesSessionFlag("claude"))
	assert.False(t, CarriesSessionFlag("claude --model opus"))

	assert.Equal(t, "claude --resume abc", Command("claude --resume abc", "def"))
	assert.Equal(t, "claude --resume abc", StartCommand("claude --resume abc", "def"))
	assert.Equal(t, "claude --session-id def", StartCommand("claude", "def"))
	assert.Equal(t, "aider", StartCommand("aider", "def"), "only Claude takes this flag")
	assert.Equal(t, "claude", StartCommand("claude", ""))
}

// `claude --session-id` requires a version 4 UUID and refuses anything else, so
// a malformed id would fail in the pane rather than here.
func TestNewSessionIDIsAV4UUID(t *testing.T) {
	v4 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

	seen := make(map[string]bool, 64)
	for range 64 {
		id, err := NewSessionID()
		require.NoError(t, err)
		assert.Regexp(t, v4, id)
		require.False(t, seen[id], "ids must not repeat: a reused one is refused as already taken")
		seen[id] = true
	}
}
