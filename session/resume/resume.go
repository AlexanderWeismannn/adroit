// Package resume brings back the Claude conversation of a session that no
// longer exists.
//
// Killing a session destroys its tmux session, its worktree and its branch, but
// not its transcript: Claude Code writes one JSONL file per conversation under
// ~/.claude/projects/<slug of the working directory>/<session id>.jsonl, and
// that file outlives everything Adroit tears down. Restoring a killed session is
// therefore not a git operation at all -- it is a new session whose program is
// `claude --resume <session id>`.
//
// The one wrinkle is that Claude looks for a session id only in the project
// directory belonging to the CURRENT working directory, and a restored session
// gets a brand new worktree path (the path carries a nanosecond suffix, so it is
// never the same twice). So Install copies the transcript into the new
// directory's project folder before the program starts. Copying rather than
// moving: the original conversation stays where it is, and a restore that fails
// half way has taken nothing away.
package resume

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxSummaryScan bounds how far into a transcript we look for something to label
// it with. The first human message is within the first few records in practice;
// a transcript can be megabytes, and reading all of it to label one row in a
// picker is not worth it.
const maxSummaryScan = 400

// summaryWidth is how much of the first message a row shows.
const summaryWidth = 72

// Transcript is one Claude conversation on disk.
type Transcript struct {
	// SessionID is what `claude --resume` takes. It is also the file's name.
	SessionID string
	// Path is the .jsonl itself.
	Path string
	// CWD is the directory the conversation ran in, as recorded in the file --
	// the proof that this transcript really belongs to the session we asked
	// about, rather than to a directory whose slug merely looks similar.
	CWD string
	// Branch is the git branch the conversation was on, when it recorded one.
	Branch string
	// Summary is the first thing the user said, for labelling a row.
	Summary string
	// ModTime is when the conversation was last written to.
	ModTime time.Time
}

// configDir is the Claude config directory. CLAUDE_CONFIG_DIR is what Claude
// Code itself honours, so honour it here too rather than assuming ~/.claude.
func configDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not find the home directory: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}

// ProjectsDir is where Claude Code keeps one directory per project.
func ProjectsDir() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "projects"), nil
}

// Slug is the project directory name Claude Code derives from a working
// directory: every character that is not a letter or a digit becomes a dash.
// "/home/u/.adroit/worktrees/TASK-1_18d3" becomes
// "-home-u--adroit-worktrees-TASK-1-18d3".
func Slug(workdir string) string {
	var b strings.Builder
	b.Grow(len(workdir))
	for _, r := range workdir {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// ProjectDir is the directory holding the transcripts of conversations that ran
// in workdir.
func ProjectDir(workdir string) (string, error) {
	projects, err := ProjectsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(projects, Slug(workdir)), nil
}

// Find returns the most recently written transcript of a conversation that ran
// in workdir. A directory that never ran Claude has none, which is not an error
// the caller needs to shout about -- ok reports it.
func Find(workdir string) (t Transcript, ok bool) {
	dir, err := ProjectDir(workdir)
	if err != nil {
		return Transcript{}, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Transcript{}, false
	}

	type candidate struct {
		path    string
		modTime time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{filepath.Join(dir, entry.Name()), info.ModTime()})
	}
	if len(candidates) == 0 {
		return Transcript{}, false
	}
	sort.Slice(candidates, func(a, b int) bool {
		return candidates[a].modTime.After(candidates[b].modTime)
	})

	newest := candidates[0]
	transcript := Transcript{
		SessionID: strings.TrimSuffix(filepath.Base(newest.path), ".jsonl"),
		Path:      newest.path,
		ModTime:   newest.modTime,
	}
	describe(&transcript)
	return transcript, true
}

// transcriptRecord is the part of a JSONL record we read. Claude writes several
// shapes into the same file -- mode changes, file snapshots, messages -- so
// every field here is optional by design.
type transcriptRecord struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	CWD       string          `json:"cwd"`
	GitBranch string          `json:"gitBranch"`
	Message   json.RawMessage `json:"message"`
}

// describe fills in the fields that can only be had by reading the file: which
// directory and branch the conversation ran in, and the first thing the user
// actually said.
func describe(t *Transcript) {
	file, err := os.Open(t.Path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// A single record can be very large (a pasted file, a long tool result), and
	// the default 64KB buffer would stop the scan dead at the first of them.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for line := 0; line < maxSummaryScan && scanner.Scan(); line++ {
		var record transcriptRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		if t.CWD == "" && record.CWD != "" {
			t.CWD = record.CWD
		}
		if t.Branch == "" && record.GitBranch != "" {
			t.Branch = record.GitBranch
		}
		if t.Summary == "" && record.Type == "user" {
			t.Summary = firstUserText(record.Message)
		}
		if t.CWD != "" && t.Summary != "" {
			return
		}
	}
}

// firstUserText pulls displayable text out of a user record, or returns "" if
// the record is not one a human typed.
//
// The content is either a plain string or a list of blocks, and plenty of what
// arrives as a "user" record is machinery: the caveat Claude Code prepends to
// local command output, command tags, system reminders, and tool results. None
// of those describe the conversation, so they are skipped in favour of the next
// record.
func firstUserText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var message struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		return ""
	}

	var text string
	if err := json.Unmarshal(message.Content, &text); err != nil {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			return ""
		}
		for _, block := range blocks {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				text = block.Text
				break
			}
		}
	}

	text = strings.TrimSpace(text)
	if text == "" || strings.HasPrefix(text, "<") {
		return ""
	}
	return truncate(strings.Join(strings.Fields(text), " "), summaryWidth)
}

func truncate(s string, width int) string {
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}

// Install puts a transcript where a conversation running in workdir can be
// resumed from it, and reports the path it wrote.
//
// Claude resolves `--resume <id>` against the project directory of the current
// working directory and nowhere else, so a restored session -- which always gets
// a new worktree path -- cannot see the original file. Copying is what makes the
// id resolvable; the original is left untouched.
func Install(t Transcript, workdir string) (string, error) {
	if t.SessionID == "" || t.Path == "" {
		return "", fmt.Errorf("no transcript to restore")
	}
	dir, err := ProjectDir(workdir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("could not create the project directory %s: %w", dir, err)
	}

	dest := filepath.Join(dir, t.SessionID+".jsonl")
	// A conversation already resumable here is the one we would be writing, since
	// the name is the session id. Leaving it alone keeps a second restore into the
	// same directory from truncating a conversation that has since moved on.
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}

	source, err := os.Open(t.Path)
	if err != nil {
		return "", fmt.Errorf("could not read the transcript %s: %w", t.Path, err)
	}
	defer source.Close()

	// Written under a temporary name and renamed into place, so a copy
	// interrupted half way cannot leave a truncated transcript sitting at the
	// name `--resume` will find.
	temp, err := os.CreateTemp(dir, t.SessionID+".*.partial")
	if err != nil {
		return "", fmt.Errorf("could not stage the transcript in %s: %w", dir, err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	if _, err := io.Copy(temp, source); err != nil {
		temp.Close()
		return "", fmt.Errorf("could not copy the transcript: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", fmt.Errorf("could not finish writing the transcript: %w", err)
	}
	if err := os.Rename(tempName, dest); err != nil {
		return "", fmt.Errorf("could not put the transcript in place: %w", err)
	}
	return dest, nil
}

// Command is program with the flags that make it pick the conversation back up.
//
// Only Claude is given them: the id means nothing to another agent, and passing
// it on would turn a restore into a program that refuses to start. Such a
// session still comes back -- as the same program in a fresh worktree, which is
// all a restore can mean for a program whose history we do not keep.
func Command(program, sessionID string) string {
	if sessionID == "" || !IsClaude(program) || CarriesSessionFlag(program) {
		return program
	}
	return program + " --resume " + sessionID
}

// IsClaude reports whether a program string runs Claude Code. The string may
// carry flags and a path ("/usr/local/bin/claude --model opus"), so it is the
// first word's base name that decides.
func IsClaude(program string) bool {
	fields := strings.Fields(program)
	if len(fields) == 0 {
		return false
	}
	return strings.TrimSuffix(filepath.Base(fields[0]), ".exe") == "claude"
}

// NewSessionID mints a session id for a conversation that has not started yet.
//
// Adroit names the conversation instead of letting Claude name it, because the
// alternative -- finding the transcript afterwards -- cannot tell two sessions
// apart. Claude files transcripts by working directory, and several sessions
// can share one: two scratch sessions on the same repository, with no worktrees
// of their own, write into the same project folder, and "the most recently
// modified transcript there" is then whichever of them typed last. Naming it up
// front is the only way the answer stays right.
//
// A version 4 UUID, which is what `claude --session-id` requires.
func NewSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("could not generate a session id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// TranscriptPath is where Claude Code keeps the transcript of sessionID for a
// conversation that ran in workdir.
func TranscriptPath(workdir, sessionID string) (string, error) {
	dir, err := ProjectDir(workdir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sessionID+".jsonl"), nil
}

// Exists reports whether that transcript is on disk and has something in it.
//
// Emptiness matters: `claude --session-id` writes the file as soon as the
// program starts, so its mere presence says only that Claude was launched, not
// that anything was said. Resuming an empty conversation fails and leaves the
// pane showing an error instead of an agent.
func Exists(workdir, sessionID string) bool {
	if sessionID == "" {
		return false
	}
	path, err := TranscriptPath(workdir, sessionID)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

// sessionFlags are the flags that already decide which conversation a command
// line runs. A program carrying one of them has been aimed at a conversation by
// somebody else -- the killed-session restore builds `--resume <id>`, and a user
// is free to put one in their configured program -- and must be left alone.
var sessionFlags = map[string]bool{
	"--session-id":   true,
	"--resume":       true,
	"-r":             true,
	"--continue":     true,
	"-c":             true,
	"--fork-session": true,
	"--from-pr":      true,
	"--teleport":     true,
}

// CarriesSessionFlag reports whether a program string already names the
// conversation to run.
func CarriesSessionFlag(program string) bool {
	for _, field := range strings.Fields(program) {
		// --resume=<id> as well as --resume <id>.
		if name, _, found := strings.Cut(field, "="); found {
			field = name
		}
		if sessionFlags[field] {
			return true
		}
	}
	return false
}

// StartCommand is the command line that starts a NEW conversation under a
// session id we have chosen.
func StartCommand(program, sessionID string) string {
	if sessionID == "" || !IsClaude(program) || CarriesSessionFlag(program) {
		return program
	}
	return program + " --session-id " + sessionID
}

// Sole returns the one conversation that ran in workdir, when there is exactly
// one.
//
// It exists for the sessions that pre-date Adroit naming its own conversations:
// they have no recorded id, and after a crash there is nothing to resume by.
// Reading the id back off disk answers that -- but only when the answer is not
// a guess. "Exactly one" is the whole condition. Claude files transcripts by
// working directory, so two sessions that share a directory share a folder, and
// picking the most recently written of several would hand one session's
// conversation to another. A fresh conversation is a small loss; the wrong
// conversation is a confusing one.
func Sole(workdir string) (Transcript, bool) {
	dir, err := ProjectDir(workdir)
	if err != nil {
		return Transcript{}, false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Transcript{}, false
	}

	var only string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		if only != "" {
			return Transcript{}, false
		}
		only = entry.Name()
	}
	if only == "" {
		return Transcript{}, false
	}

	path := filepath.Join(dir, only)
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return Transcript{}, false
	}
	transcript := Transcript{
		SessionID: strings.TrimSuffix(only, ".jsonl"),
		Path:      path,
		ModTime:   info.ModTime(),
	}
	describe(&transcript)
	if transcript.CWD != workdir {
		// A CWD that does not match means the slug collided, or the directory was
		// reused: the transcript is somebody else's.
		return Transcript{}, false
	}
	return transcript, true
}
