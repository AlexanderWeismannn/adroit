package log

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The three loggers are never nil. Initialize points them at the log file; until
// it is called they discard, because the alternative is that any code path
// reached without it -- every test that touches a package which logs -- panics
// inside log.Printf on a nil receiver, and does so in whatever unrelated thing
// happened to call it.
var (
	WarningLog = discardLogger()
	InfoLog    = discardLogger()
	ErrorLog   = discardLogger()
)

func discardLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

// logFileName is where the log goes. A test binary writes somewhere else: the
// suites initialise logging too, and their deliberate failures ("boom", a
// "dead-session", a program that does not exist) used to land in the same file
// the real interface logs to, where they read as incidents.
var logFileName = func() string {
	if testing.Testing() {
		return filepath.Join(os.TempDir(), "adroit-test.log")
	}
	return filepath.Join(os.TempDir(), "adroit.log")
}()

// Rotation: checked once at startup, since a session can run for weeks and the
// file was only ever appended to. Keeps logKeep old files beside the live one.
const (
	logRotateBytes = 10 << 20
	logKeep        = 3
)

var globalLogFile *os.File

// rotate shifts adroit.log to adroit.log.1 (and .1 to .2, ...) once it is over
// logRotateBytes. Best effort: a log that cannot be rotated is still a log.
func rotate(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < logRotateBytes {
		return
	}
	for n := logKeep - 1; n >= 1; n-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", path, n), fmt.Sprintf("%s.%d", path, n+1))
	}
	_ = os.Rename(path, path+".1")
}

// Initialize should be called once at the beginning of the program to set up logging.
// defer Close() after calling this function. It sets the go log output to the file in
// the os temp directory.

func Initialize(daemon bool) {
	rotate(logFileName)
	// 0600, and tightened if the file already exists: it records paths, branch
	// names and command lines, in a directory everyone can read.
	f, err := os.OpenFile(logFileName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		panic(fmt.Sprintf("could not open log file: %s", err))
	}
	_ = f.Chmod(0600)

	// Set log format to include timestamp and file/line number
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	fmtS := "%s"
	if daemon {
		fmtS = "[DAEMON] %s"
	}
	InfoLog = log.New(f, fmt.Sprintf(fmtS, "INFO:"), log.Ldate|log.Ltime|log.Lshortfile)
	WarningLog = log.New(f, fmt.Sprintf(fmtS, "WARNING:"), log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLog = log.New(f, fmt.Sprintf(fmtS, "ERROR:"), log.Ldate|log.Ltime|log.Lshortfile)

	globalLogFile = f
}

func Close() {
	if globalLogFile == nil {
		return
	}
	_ = globalLogFile.Close()
	// TODO: maybe only print if verbose flag is set?
	fmt.Println("wrote logs to " + logFileName)
}

// CloseQuietly closes the log without announcing it, for commands whose whole
// output is a report and where the extra line is noise.
func CloseQuietly() {
	if globalLogFile != nil {
		_ = globalLogFile.Close()
	}
}

// Every is used to log at most once every timeout duration.
type Every struct {
	timeout time.Duration
	timer   *time.Timer
}

func NewEvery(timeout time.Duration) *Every {
	return &Every{timeout: timeout}
}

// ShouldLog returns true if the timeout has passed since the last log.
func (e *Every) ShouldLog() bool {
	if e.timer == nil {
		e.timer = time.NewTimer(e.timeout)
		e.timer.Reset(e.timeout)
		return true
	}

	select {
	case <-e.timer.C:
		e.timer.Reset(e.timeout)
		return true
	default:
		return false
	}
}
