package dev

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/AlexanderWeismannn/adroit/log"
)

var (
	wslOnce sync.Once
	isWSL   bool
)

// underWSL reports whether we are running in WSL, where none of the usual Linux
// openers exist and the browser lives on the Windows side.
func underWSL() bool {
	wslOnce.Do(func() {
		if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
			isWSL = true
			return
		}
		// WSL1 sets neither; its kernel string is the reliable tell.
		if b, err := os.ReadFile("/proc/version"); err == nil {
			v := strings.ToLower(string(b))
			isWSL = strings.Contains(v, "microsoft") || strings.Contains(v, "wsl")
		}
	})
	return isWSL
}

// windowsOpeners are tried in order under WSL. explorer.exe is first because it
// hands the URL to the user's default browser with no console window; the
// PowerShell fallback covers a distro with interop restricted to /mnt/c paths.
var windowsOpeners = [][]string{
	{"/mnt/c/Windows/explorer.exe"},
	{"explorer.exe"},
	{"/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe", "-NoProfile", "-Command", "Start-Process"},
	{"powershell.exe", "-NoProfile", "-Command", "Start-Process"},
}

// OpenURL opens url with the configured command, or a platform default. The URL
// is appended as the final argument.
func OpenURL(url string, command []string) error {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	if len(command) > 0 {
		return runOpener(append(append([]string(nil), command...), url))
	}

	for _, opener := range defaultOpeners() {
		if _, err := exec.LookPath(opener[0]); err != nil {
			continue
		}
		if err := runOpener(append(append([]string(nil), opener...), url)); err != nil {
			log.InfoLog.Printf("dev stack: opener %s failed: %v", opener[0], err)
			continue
		}
		return nil
	}
	return fmt.Errorf("no way to open a browser: set dev.open_command in the Adroit config")
}

func defaultOpeners() [][]string {
	if underWSL() {
		// xdg-open last: a WSL box may have it installed and pointed at nothing,
		// in which case it succeeds and no window appears.
		return append(windowsOpeners, []string{"xdg-open"})
	}
	switch runtime.GOOS {
	case "darwin":
		return [][]string{{"open"}}
	case "windows":
		return [][]string{{"cmd", "/c", "start", ""}}
	default:
		return [][]string{{"xdg-open"}, {"gio", "open"}, {"x-www-browser"}}
	}
}

// runOpener runs the opener and decides whether it worked.
//
// explorer.exe exits 1 on SUCCESS -- it reports whether it opened a window of
// its own, not whether it handed the URL off -- so a plain error check makes
// every successful open look like a failure and sends us down the fallback
// chain, opening the page two or three more times.
func runOpener(argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if isExplorer(argv[0]) {
		if code := cmd.ProcessState.ExitCode(); code == 1 {
			return nil
		}
	}
	if detail := strings.TrimSpace(string(out)); detail != "" {
		return fmt.Errorf("%s: %s", argv[0], firstLine(detail))
	}
	return fmt.Errorf("%s: %w", argv[0], err)
}

func isExplorer(path string) bool {
	return strings.EqualFold(filepathBase(path), "explorer.exe")
}

func filepathBase(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
