package app

import (
	"io"

	"github.com/AlexanderWeismannn/adroit/session/tmux"

	tea "github.com/charmbracelet/bubbletea"
)

// attachFinishedMsg is delivered once the user has detached from a session.
type attachFinishedMsg struct{ err error }

// attachCommand adapts an attach to bubbletea's ExecCommand, whose contract is
// "take the terminal, block until done, give it back".
//
// The stream setters are deliberately empty. An ExecCommand is normally an
// exec.Cmd that has to be told where its stdio goes; the attach talks to
// os.Stdin and os.Stdout directly, through the PTY the tmux client is on, and
// those are the same two files bubbletea would hand over here.
type attachCommand struct {
	attach func() (chan struct{}, error)
	// terminating is closed when Adroit has been told to quit; see
	// handleTermination.
	terminating <-chan struct{}
}

func (c *attachCommand) Run() error {
	select {
	case <-c.terminating:
		return nil
	default:
	}
	ch, err := c.attach()
	if err != nil {
		return err
	}
	select {
	case <-ch:
	case <-c.terminating:
		tmux.DetachAttached()
		<-ch
	}
	return nil
}

func (c *attachCommand) SetStdin(io.Reader) {}

func (c *attachCommand) SetStdout(io.Writer) {}

func (c *attachCommand) SetStderr(io.Writer) {}

// attachCmd hands the terminal to a session for as long as the user is
// attached to it.
//
// tea.Exec rather than releasing the terminal by hand, and this is the whole
// reason: bubbletea dispatches an Exec from its event loop, not from Update.
// Attaching from inside Update cannot release the terminal cleanly, because
// bubbletea's input reader delivers keystrokes over an unbuffered channel and
// blocks on the send -- and while Update is running, nothing is receiving.
// ReleaseTerminal cancels the reader, but a cancel does not interrupt a
// channel send, so it gives up after 500ms and leaves the reader alive. The
// moment Update returns and the event loop drains that send, the reader loops
// round and reads stdin again, swallowing exactly one keystroke: the first
// thing typed into a freshly attached session, gone, every time the attach was
// reached through a help screen. From the event loop the send has already
// landed, the reader is idle in a read, and the cancel works.
func (m *home) attachCmd(attach func() (chan struct{}, error)) tea.Cmd {
	return tea.Exec(&attachCommand{attach: attach, terminating: m.terminating}, func(err error) tea.Msg {
		return attachFinishedMsg{err: err}
	})
}
