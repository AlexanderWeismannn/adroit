package dev

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AlexanderWeismannn/adroit/log"
)

// This file is the whole of the dev stack's dependence on how the operating
// system exposes processes and sockets. Everything else asks it four questions:
// which pids listen on a port, what a pid's working directory is, who each pid's
// parent is, and which pids are still alive.
//
// Two answers to each. Linux (and WSL) reads /proc and `ss`, which is exact and
// costs no subprocess for the two questions that matter most. macOS has neither,
// so it goes through `lsof` and `ps`, which every install has.
//
// Picked at RUNTIME rather than with build tags, so the fallbacks compile and
// are testable on the machine this is developed on -- a path that only builds on
// hardware nobody here has is a path that rots.

// hasProcFS is whether this system has a Linux-style /proc. Probed once via a
// file every procfs has and no other filesystem would fake.
var hasProcFS = func() bool {
	_, err := os.Stat("/proc/self/stat")
	return err == nil
}()

// ssPidRe pulls the pid out of an `ss` users: field, e.g. users:(("node",pid=8123,fd=20)).
var ssPidRe = regexp.MustCompile(`pid=(\d+)`)

// listenerTTL is how long one listing answers for.
//
// The listing is system-wide, so every own_cwd check in a poll is asking the
// same question, and a listener's pid does not change while it holds the port.
// Without the cache each check spawned its own scan -- two per poll here, twice
// a second -- to parse the same table twice. Short enough that a server
// restarting on the same port is attributed correctly within one poll.
const listenerTTL = 2 * time.Second

var (
	lsnMu    sync.Mutex
	lsnAt    time.Time
	lsnTable map[string][]int
	lsnErr   error
)

// listenerPIDs returns the pids listening on port, reusing the last listing
// while it is younger than listenerTTL. force skips the cache, for the caller
// that has already proved the cached answer cannot be right.
//
// found distinguishes "nothing is listed for this port" from "listed, but its
// owner could not be read": the first means the listing is stale or the port is
// not held, the second that the owner is real but unreadable.
func listenerPIDs(port string, force bool) (pids []int, found bool, err error) {
	lsnMu.Lock()
	defer lsnMu.Unlock()

	if force || lsnTable == nil || time.Since(lsnAt) >= listenerTTL {
		lsnTable, lsnErr = scanListeners()
		lsnAt = time.Now()
	}
	if lsnErr != nil {
		return nil, false, lsnErr
	}
	pids, found = lsnTable[port]
	return pids, found, nil
}

// listenerTools are tried in order. `ss` first because it is one syscall-cheap
// read of /proc/net on the platform that has it; `lsof` is the portable
// fallback, and the only one on macOS. A var so a test can force the fallback on
// a machine that has both -- otherwise the macOS path would only ever be
// exercised on macOS, which is where it would be discovered broken.
var listenerTools = []string{"ss", "lsof"}

// scanListeners builds the port -> pids table for every listening TCP socket.
func scanListeners() (map[string][]int, error) {
	for _, tool := range listenerTools {
		path, err := lookTool(tool)
		if err != nil {
			continue
		}
		table, err := runListenerTool(tool, path)
		if err != nil {
			log.InfoLog.Printf("dev stack: %s failed, trying the next: %v", tool, err)
			continue
		}
		return table, nil
	}
	return nil, errNoListenerTool
}

func runListenerTool(tool, path string) (map[string][]int, error) {
	switch tool {
	case "ss":
		// -H drops the header, -n keeps ports numeric, -p names the owning process.
		out, err := exec.Command(path, "-ltnpH").Output()
		if err != nil {
			return nil, err
		}
		return parseSSListeners(string(out)), nil
	case "lsof":
		// -F is the machine-readable format: one field per line, tagged by its
		// first character. p is a pid, n the socket's name -- which carries the
		// port. lsof exits 1 when nothing matches, which is an empty table rather
		// than a failure, so the output is parsed either way.
		out, _ := exec.Command(path, "-nP", "-iTCP", "-sTCP:LISTEN", "-Fpn").Output()
		return parseLsofListeners(string(out)), nil
	}
	return nil, errNoListenerTool
}

// errNoListenerTool is returned when neither ss nor lsof is installed. own_cwd
// is the only feature that needs them, and it degrades to "owner not resolved"
// rather than to a red lamp.
var errNoListenerTool = errNoTool("neither ss nor lsof is available, port ownership cannot be verified")

type errNoTool string

func (e errNoTool) Error() string { return string(e) }

// parseSSListeners reads `ss -ltnpH` output.
func parseSSListeners(out string) map[string][]int {
	table := make(map[string][]int)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		// Local address is the 4th column: State Recv-Q Send-Q Local Peer [users]
		if len(fields) < 4 {
			continue
		}
		port := portOf(fields[3])
		if port == "" {
			continue
		}
		for _, m := range ssPidRe.FindAllStringSubmatch(line, -1) {
			if pid, err := strconv.Atoi(m[1]); err == nil {
				table[port] = appendUnique(table[port], pid)
			}
		}
		// A port with no readable owner is still a port that is held; recording it
		// with no pids is what lets the caller tell that apart from an empty table.
		if _, ok := table[port]; !ok {
			table[port] = nil
		}
	}
	return table
}

// parseLsofListeners reads `lsof -Fpn` output: `p<pid>` opens a process's
// section and every `n<name>` under it belongs to that pid.
func parseLsofListeners(out string) map[string][]int {
	table := make(map[string][]int)
	pid := 0
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			// e.g. *:3000, 127.0.0.1:3000, [::1]:3000
			if port := portOf(line[1:]); port != "" {
				table[port] = appendUnique(table[port], pid)
			}
		}
	}
	return table
}

// processCwd returns a process's working directory, or "" when it cannot be
// read -- a process owned by another user, or one that has already exited.
func processCwd(pid int) string {
	if hasProcFS {
		link, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
		if err != nil {
			return ""
		}
		return link
	}

	path, err := lookTool("lsof")
	if err != nil {
		return ""
	}
	out, err := exec.Command(path, "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil && len(out) == 0 {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return strings.TrimSpace(line[1:])
		}
	}
	return ""
}

// processParents returns pid -> parent pid for every process that can be read.
func processParents() map[int]int {
	if hasProcFS {
		return procfsParents()
	}
	return psParents()
}

// procfsParents walks /proc, reading each process's stat file.
func procfsParents() map[int]int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		log.WarningLog.Printf("dev stack: cannot read /proc, process tree unavailable: %v", err)
		return nil
	}
	parents := make(map[int]int, len(entries))
	for _, e := range entries {
		pid, convErr := strconv.Atoi(e.Name())
		if convErr != nil {
			continue
		}
		if ppid := procfsParentOf(pid); ppid > 0 {
			parents[pid] = ppid
		}
	}
	return parents
}

// procfsParentOf reads one process's parent pid, or 0 if it cannot be determined.
func procfsParentOf(pid int) int {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	// Field 2 (comm) is parenthesised and may itself contain spaces and
	// parentheses, so the line cannot be split before the LAST closing paren.
	end := strings.LastIndex(string(data), ") ")
	if end < 0 {
		return 0
	}
	fields := strings.Fields(string(data)[end+2:])
	// After "comm) " the fields are state, ppid, pgrp, ...
	if len(fields) < 2 {
		return 0
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return ppid
}

// psParents reads the whole table in one `ps`, which is what makes this
// affordable without /proc: a call per pid would be hundreds of subprocesses.
func psParents() map[int]int {
	path, err := lookTool("ps")
	if err != nil {
		log.WarningLog.Printf("dev stack: no ps, process tree unavailable: %v", err)
		return nil
	}
	out, err := exec.Command(path, "-eo", "pid=,ppid=").Output()
	if err != nil {
		log.WarningLog.Printf("dev stack: ps failed, process tree unavailable: %v", err)
		return nil
	}
	return parsePIDPairs(string(out))
}

// parsePIDPairs reads lines of "<pid> <ppid>".
func parsePIDPairs(out string) map[int]int {
	parents := make(map[int]int)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil || pid <= 0 {
			continue
		}
		parents[pid] = ppid
	}
	return parents
}

// livePIDs returns those of pids that still exist.
func livePIDs(pids []int) []int {
	if len(pids) == 0 {
		return nil
	}
	if hasProcFS {
		var alive []int
		for _, pid := range pids {
			if _, err := os.Stat("/proc/" + strconv.Itoa(pid)); err == nil {
				alive = append(alive, pid)
			}
		}
		return alive
	}

	path, err := lookTool("ps")
	if err != nil {
		// Unknowable is not the same as dead. Reporting them all gone would end
		// the escalation from TERM to KILL before it had done anything.
		return pids
	}
	args := make([]string, 0, len(pids))
	for _, pid := range pids {
		args = append(args, strconv.Itoa(pid))
	}
	// ps exits non-zero when none of them are left, which is an empty answer
	// rather than a failure -- so the output is parsed either way.
	out, _ := exec.Command(path, "-o", "pid=", "-p", strings.Join(args, ",")).Output()

	wanted := make(map[int]bool, len(pids))
	for _, pid := range pids {
		wanted[pid] = true
	}
	var alive []int
	for _, line := range strings.Split(string(out), "\n") {
		if pid, convErr := strconv.Atoi(strings.TrimSpace(line)); convErr == nil && wanted[pid] {
			alive = append(alive, pid)
		}
	}
	return alive
}

var (
	toolMu    sync.Mutex
	toolPaths = map[string]string{}
	toolErrs  = map[string]error{}
)

// lookTool caches a PATH lookup. These are asked for on every poll, and a miss
// is the expensive case: exec.LookPath walks every PATH entry to fail.
func lookTool(name string) (string, error) {
	toolMu.Lock()
	defer toolMu.Unlock()
	if path, ok := toolPaths[name]; ok {
		return path, nil
	}
	if err, ok := toolErrs[name]; ok {
		return "", err
	}
	path, err := exec.LookPath(name)
	if err != nil {
		toolErrs[name] = err
		log.WarningLog.Printf("dev stack: `%s` not on PATH: %v", name, err)
		return "", err
	}
	toolPaths[name] = path
	return path, nil
}

func appendUnique(pids []int, pid int) []int {
	if pid <= 0 {
		return pids
	}
	for _, p := range pids {
		if p == pid {
			return pids
		}
	}
	return append(pids, pid)
}
