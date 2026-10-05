package app

import (
	"fmt"
	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/log"
	"github.com/AlexanderWeismannn/adroit/session"
	"github.com/AlexanderWeismannn/adroit/session/resume"
	"github.com/AlexanderWeismannn/adroit/ui"
	"github.com/AlexanderWeismannn/adroit/ui/overlay"
	"os"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Killing a session destroys its worktree and its branch, and there is no
// undoing that -- but the conversation it was having is not in either of them.
// Claude keeps every transcript outside the repository, so what a kill really
// costs, and what this file gives back, is the ability to carry on talking to a
// session you did not mean to end. A restored session is a NEW session: new
// worktree, new branch off HEAD, running the same program with the old
// conversation resumed in it.

// recordKilledSession remembers what is needed to start a killed session's
// conversation again.
//
// Called before anything is torn down, because the worktree path is what finds
// the transcript and a killed instance no longer has one. Nothing here may fail
// the kill: the user asked for the session to go, and a bookkeeping error is not
// a reason to keep it.
func (m *home) recordKilledSession(instance *session.Instance) {
	if m.appState == nil || instance == nil {
		return
	}

	record := config.KilledSession{
		Title:    instance.Title,
		Branch:   instance.Branch,
		Program:  instance.Program,
		RepoPath: instance.Path,
		KilledAt: time.Now(),
	}

	// Where the program was actually running: a worktree of its own, or the
	// repository itself for a session that has none. That directory is what
	// Claude named its project folder after.
	workdir := instance.GetWorktreePath()
	if workdir == "" {
		workdir = instance.Path
	}
	if worktree, err := instance.GetGitWorktree(); err == nil {
		record.RepoPath = worktree.GetRepoPath()
	}

	if transcript, ok := resume.Find(workdir); ok {
		record.SessionID = transcript.SessionID
		record.TranscriptPath = transcript.Path
		record.Summary = transcript.Summary
		if record.Branch == "" {
			record.Branch = transcript.Branch
		}
	}

	if err := m.appState.AddKilledSession(record); err != nil {
		log.WarningLog.Printf("could not record killed session %q for restore: %v", instance.Title, err)
		return
	}
	m.menu.SetRestorable(true)
}

// openRestorePicker shows the killed sessions, most recently killed first.
func (m *home) openRestorePicker() (tea.Model, tea.Cmd) {
	if m.appState == nil {
		return m, nil
	}
	records := m.appState.GetKilledSessions()
	if len(records) == 0 {
		return m, m.handleError(fmt.Errorf("no killed sessions to restore"))
	}

	entries := make([]overlay.RestoreEntry, 0, len(records))
	for _, record := range records {
		entries = append(entries, overlay.RestoreEntry{
			Title:    record.Title,
			Branch:   record.Branch,
			Summary:  record.Summary,
			KilledAt: record.KilledAt,
			// Checked against the disk rather than trusted from the record: a
			// transcript can be deleted between the kill and the restore, and a row
			// that promises a conversation it cannot produce is worse than one that
			// says it will start fresh.
			Resumable: transcriptExists(record),
		})
	}

	m.restorePicker = overlay.NewRestorePicker(entries)
	if m.termWidth > 0 {
		// Two columns of border and padding on each side, plus a little air.
		m.restorePicker.SetWidth(min(74, m.termWidth-4))
	}
	m.state = stateRestore
	return m, nil
}

func transcriptExists(record config.KilledSession) bool {
	if record.SessionID == "" || record.TranscriptPath == "" {
		return false
	}
	_, err := os.Stat(record.TranscriptPath)
	return err == nil
}

// handleRestoreState drives the picker: moving changes nothing but the cursor,
// enter starts the chosen session, and every way out leaves the list untouched.
func (m *home) handleRestoreState(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.restorePicker == nil {
		m.state = stateDefault
		return m, nil
	}

	// ctrl+c closes the picker rather than quitting, matching the other overlays.
	if msg.String() == "ctrl+c" {
		return m, m.closeRestorePicker()
	}

	switch m.restorePicker.HandleKeyPress(msg) {
	case overlay.PickerCancel:
		return m, m.closeRestorePicker()
	case overlay.PickerConfirm:
		entry, ok := m.restorePicker.Selected()
		if !ok {
			return m, m.closeRestorePicker()
		}
		record, found := m.killedSessionFor(entry)
		closeCmd := m.closeRestorePicker()
		if !found {
			return m, tea.Batch(closeCmd, m.handleError(fmt.Errorf("could not find %q to restore", entry.Title)))
		}
		model, restoreCmd := m.restoreSession(record)
		return model, tea.Batch(closeCmd, restoreCmd)
	}
	return m, nil
}

// killedSessionFor maps the chosen row back to the record behind it. The picker
// holds display strings only, so the state is re-read here rather than a record
// being carried through the overlay.
func (m *home) killedSessionFor(entry overlay.RestoreEntry) (config.KilledSession, bool) {
	for _, record := range m.appState.GetKilledSessions() {
		if record.Title == entry.Title && record.KilledAt.Equal(entry.KilledAt) {
			return record, true
		}
	}
	return config.KilledSession{}, false
}

func (m *home) closeRestorePicker() tea.Cmd {
	m.restorePicker = nil
	m.state = stateDefault
	return tea.Sequence(
		tea.WindowSize(),
		func() tea.Msg {
			m.menu.SetState(ui.StateDefault)
			return nil
		},
	)
}

// restoreSession starts a new session that picks up a killed one's conversation.
//
// It goes through the same door as pressing n and then enter -- Loading row,
// finalize, start in the background -- so everything downstream of a new session
// (the help screen, the storage write, the error path that removes a row that
// failed to start) applies here without a second implementation of any of it.
func (m *home) restoreSession(record config.KilledSession) (tea.Model, tea.Cmd) {
	if m.list.NumInstances() >= GlobalInstanceLimit {
		return m, m.handleError(
			fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
	}

	program := record.Program
	if program == "" {
		program = m.program
	}
	path := record.RepoPath
	if path == "" {
		path = "."
	}

	resumable := transcriptExists(record)
	sessionID := ""
	if resumable {
		sessionID = record.SessionID
	}

	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   m.uniqueTitle(record.Title),
		Path:    path,
		Program: resume.Command(program, sessionID),
	})
	if err != nil {
		return m, m.handleError(err)
	}

	if resumable {
		transcript := resume.Transcript{SessionID: record.SessionID, Path: record.TranscriptPath}
		// The worktree path is settled inside Start, and carries a timestamp, so
		// this is the only moment at which the conversation can be put where the
		// program will look for it.
		instance.SetOnWorkspaceReady(func(dir string) error {
			_, err := resume.Install(transcript, dir)
			return err
		})
	}

	finalize := m.list.AddInstance(instance)
	m.list.SetSelectedInstance(m.list.NumInstances() - 1)
	instance.SetStatus(session.Loading)
	finalize()
	m.menu.SetState(ui.StateDefault)

	startCmd := func() tea.Msg {
		err := instance.Start(true)
		return instanceStartedMsg{instance: instance, err: err}
	}
	return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
}

// uniqueTitle keeps a restored session from colliding with one already in the
// list. The title names the tmux session and the branch, so a duplicate does not
// merely look confusing -- tmux refuses the second session outright.
func (m *home) uniqueTitle(title string) string {
	if title == "" {
		title = "restored"
	}
	taken := make(map[string]bool, m.list.NumInstances())
	for _, instance := range m.list.GetInstances() {
		taken[instance.Title] = true
	}
	if !taken[title] {
		return title
	}
	for suffix := 2; ; suffix++ {
		candidate := trimTitle(title, len(strconv.Itoa(suffix))+1) + "-" + strconv.Itoa(suffix)
		if !taken[candidate] {
			return candidate
		}
	}
}

// trimTitle makes room for a suffix within the 32-character limit the naming
// prompt enforces.
func trimTitle(title string, room int) string {
	runes := []rune(title)
	if len(runes)+room <= 32 {
		return title
	}
	return string(runes[:32-room])
}
