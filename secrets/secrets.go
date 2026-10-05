// Package secrets keeps the API keys agents need, out of config.json.
//
// A key is stored under a name ("codex/OPENAI_API_KEY": the profile, then the
// environment variable the agent reads it from) in the first backend that works
// here: the macOS Keychain, the Linux Secret Service, or a file in the config
// directory that only you can read. Values never pass through a command line,
// where any process on the machine could read them out of ps.
package secrets

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/AlexanderWeismannn/adroit/config"
)

// service is the name entries are filed under in the system keyring.
const service = "adroit"

// FileName is the fallback store inside the config directory.
const FileName = "secrets.env"

// ErrNotFound is returned by Get for a name with no stored value.
var ErrNotFound = errors.New("no stored value")

// Store is somewhere keys can be kept.
type Store interface {
	Get(name string) (string, error)
	Set(name, value string) error
	Delete(name string) error
	// Backend names where values go, for the settings screen to show.
	Backend() string
}

// Key is the name a profile's environment variable is stored under.
func Key(profile, envVar string) string { return profile + "/" + envVar }

var (
	defaultOnce  sync.Once
	defaultStore Store
)

// Default is the best store available on this machine, chosen once.
func Default() Store {
	defaultOnce.Do(func() { defaultStore = choose() })
	return defaultStore
}

func choose() Store {
	file := &fileStore{}
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("security"); err == nil {
			return &fallback{primary: keychain{}, file: file}
		}
	case "linux":
		// Secret Service needs a session bus and a running keyring; a headless
		// box or WSL usually has neither, and secret-tool then fails on every
		// call. Those land in the file instead.
		if _, err := exec.LookPath("secret-tool"); err == nil && os.Getenv("DBUS_SESSION_BUS_ADDRESS") != "" {
			return &fallback{primary: secretService{}, file: file}
		}
	}
	return file
}

// fallback uses the system keyring and drops to the file when the keyring
// refuses -- locked, no daemon, no permission -- rather than losing the key.
type fallback struct {
	primary Store
	file    *fileStore
}

func (f *fallback) Get(name string) (string, error) {
	v, err := f.primary.Get(name)
	if err == nil {
		return v, nil
	}
	if fv, ferr := f.file.Get(name); ferr == nil {
		return fv, nil
	}
	return "", err
}

func (f *fallback) Set(name, value string) error {
	if err := f.primary.Set(name, value); err != nil {
		if ferr := f.file.Set(name, value); ferr != nil {
			return fmt.Errorf("%v; and the file fallback failed too: %w", err, ferr)
		}
		return nil
	}
	// The keyring has it now; a copy left in the file would outlive a later
	// change made through the keyring.
	_ = f.file.Delete(name)
	return nil
}

func (f *fallback) Delete(name string) error {
	err := f.primary.Delete(name)
	ferr := f.file.Delete(name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if ferr != nil && !errors.Is(ferr, ErrNotFound) {
		return ferr
	}
	return nil
}

func (f *fallback) Backend() string { return f.primary.Backend() }

// keychain is the macOS login keychain, through security(1). Values are fed on
// stdin to `security -i`, never as arguments.
type keychain struct{}

func (keychain) Backend() string { return "macOS Keychain" }

func (keychain) Get(name string) (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-a", name, "-w").Output()
	if err != nil {
		return "", ErrNotFound
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func (keychain) Set(name, value string) error {
	if strings.ContainsAny(value, "\n\r") {
		return fmt.Errorf("a key cannot contain a line break")
	}
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n",
		quoteForSecurity(service), quoteForSecurity(name), quoteForSecurity(value)))
	if out, err := cmd.CombinedOutput(); err != nil || bytes.Contains(out, []byte("rror")) {
		return fmt.Errorf("keychain refused the key: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func (keychain) Delete(name string) error {
	if err := exec.Command("security", "delete-generic-password", "-s", service, "-a", name).Run(); err != nil {
		return ErrNotFound
	}
	return nil
}

// quoteForSecurity quotes a word for security(1)'s interactive mode, which
// splits on whitespace and honours double quotes with backslash escapes.
func quoteForSecurity(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// secretService is the freedesktop Secret Service (GNOME Keyring, KWallet),
// through secret-tool, which reads the value from stdin.
type secretService struct{}

func (secretService) Backend() string { return "Secret Service keyring" }

func (secretService) Get(name string) (string, error) {
	out, err := exec.Command("secret-tool", "lookup", "service", service, "account", name).Output()
	if err != nil || len(out) == 0 {
		return "", ErrNotFound
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func (secretService) Set(name, value string) error {
	cmd := exec.Command("secret-tool", "store", "--label", "Adroit: "+name, "service", service, "account", name)
	cmd.Stdin = strings.NewReader(value)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("keyring refused the key: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (secretService) Delete(name string) error {
	if err := exec.Command("secret-tool", "clear", "service", service, "account", name).Run(); err != nil {
		return ErrNotFound
	}
	return nil
}

// fileStore keeps NAME=value lines in ~/.adroit/secrets.env, mode 0600.
type fileStore struct{ mu sync.Mutex }

func (*fileStore) Backend() string { return "~/.adroit/" + FileName + " (readable only by you)" }

func filePath() (string, error) {
	dir, err := config.GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

func (f *fileStore) read() (map[string]string, error) {
	path, err := filePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && k != "" {
			values[k] = v
		}
	}
	return values, sc.Err()
}

func (f *fileStore) write(values map[string]string) error {
	path, err := filePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	names := make([]string, 0, len(values))
	for k := range values {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# API keys for Adroit's agents. Managed by the settings screen (s).\n")
	for _, k := range names {
		fmt.Fprintf(&b, "%s=%s\n", k, values[k])
	}
	// Written whole and renamed into place, created 0600 from the start: a key
	// must never sit in a file anyone else can read, not even for a moment.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".secrets-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (f *fileStore) Get(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	values, err := f.read()
	if err != nil {
		return "", err
	}
	v, ok := values[name]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *fileStore) Set(name, value string) error {
	if strings.ContainsAny(value, "\n\r") || strings.ContainsAny(name, "=\n\r") {
		return fmt.Errorf("a key or its name cannot contain a line break or '=' in the name")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	values, err := f.read()
	if err != nil {
		return err
	}
	values[name] = value
	return f.write(values)
}

func (f *fileStore) Delete(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	values, err := f.read()
	if err != nil {
		return err
	}
	if _, ok := values[name]; !ok {
		return ErrNotFound
	}
	delete(values, name)
	return f.write(values)
}

// Mask shows enough of a key to recognise it and none of it to use it.
func Mask(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return strings.Repeat("•", len(value))
	}
	return value[:3] + strings.Repeat("•", 6) + value[len(value)-4:]
}
