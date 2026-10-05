package dev

import (
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/AlexanderWeismannn/adroit/config"
)

// LampState is what one readiness check currently reports.
type LampState int

const (
	// LampPending is a check that has not passed yet and is still expected to.
	LampPending LampState = iota
	// LampUp is a check that passed.
	LampUp
	// LampDown is a check that failed for a reason worth showing.
	LampDown
)

// Lamp is one check's current verdict.
type Lamp struct {
	Name   string
	State  LampState
	Detail string
}

// Up reports whether every lamp passed.
func Up(lamps []Lamp) bool {
	if len(lamps) == 0 {
		return false
	}
	for _, l := range lamps {
		if l.State != LampUp {
			return false
		}
	}
	return true
}

const probeTimeout = 1500 * time.Millisecond

// probeClient deliberately does not follow redirects: a 302 proves the server is
// answering, which is all an "is it listening" check is asking.
var probeClient = &http.Client{
	Timeout: probeTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// probe runs one check against a stack rooted at worktree.
func probe(c config.DevCheck, worktree string) Lamp {
	lamp := Lamp{Name: c.Name}

	switch c.Type {
	case "tcp":
		conn, err := net.DialTimeout("tcp", c.Target, probeTimeout)
		if err != nil {
			lamp.State = LampPending
			lamp.Detail = "not listening"
			return lamp
		}
		_ = conn.Close()
	case "http":
		resp, err := probeClient.Get(c.Target)
		if err != nil {
			lamp.State = LampPending
			lamp.Detail = "no response"
			return lamp
		}
		_ = resp.Body.Close()
		// ExpectStatus zero accepts any response: a 401 or a 404 proves a server
		// is answering as well as a 200 does, and most dev routes are authed.
		if c.ExpectStatus != 0 && resp.StatusCode != c.ExpectStatus {
			lamp.State = LampPending
			lamp.Detail = fmt.Sprintf("HTTP %d, want %d", resp.StatusCode, c.ExpectStatus)
			return lamp
		}
	default:
		lamp.State = LampDown
		lamp.Detail = fmt.Sprintf("unknown check type %q", c.Type)
		return lamp
	}

	// Something is answering. Whether it is OUR something is a separate question,
	// and the one that matters after a switch: the previous worktree's server
	// holds the same port and answers exactly the same way.
	if c.OwnCwd && worktree != "" {
		owner, err := portOwnerCwd(c.Target)
		switch {
		case err != nil:
			// Ownership is unverifiable here, not violated. Reporting the port as
			// down would be a lie; reporting it up silently would hide the one
			// thing this check exists for. Say so.
			lamp.State = LampUp
			lamp.Detail = "up (owner unknown)"
			return lamp
		case owner == "":
			lamp.State = LampPending
			lamp.Detail = "listening, owner not resolved yet"
			return lamp
		case !isUnder(owner, worktree):
			lamp.State = LampDown
			lamp.Detail = "held by another worktree: " + shorten(owner)
			return lamp
		}
	}

	lamp.State = LampUp
	lamp.Detail = "up"
	return lamp
}

// portOwnerCwd returns the working directory of the process listening on target,
// or "" when nothing could be attributed. target is host:port, or a URL.
//
// How the listeners and the working directory are actually read is procinfo.go's
// problem; on a system with neither /proc nor lsof the answer is simply
// unavailable, and own_cwd degrades to "owner not resolved" rather than to a red
// lamp.
func portOwnerCwd(target string) (string, error) {
	port := portOf(target)
	if port == "" {
		return "", fmt.Errorf("cannot read a port out of %q", target)
	}

	// The dial that got us here proved something is listening, so a listing
	// without this port in it is a stale listing rather than an answer -- a
	// server that has only just bound the port. Refresh once and look again;
	// the steady state is a hit on the first pass.
	pids, found, err := listenerPIDs(port, false)
	if err != nil {
		return "", err
	}
	if !found {
		if pids, _, err = listenerPIDs(port, true); err != nil {
			return "", err
		}
	}

	// A port can be bound on several addresses by several pids, and a process we
	// cannot read is one we cannot attribute -- so keep looking rather than
	// taking the first one's silence for an answer.
	for _, pid := range pids {
		if cwd := processCwd(pid); cwd != "" {
			return cwd, nil
		}
	}
	return "", nil
}

// portOf extracts the port from host:port, [::1]:port or a URL.
func portOf(s string) string {
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if j := strings.IndexAny(s, "/?#"); j >= 0 {
			s = s[:j]
		}
	}
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return ""
	}
	port := s[i+1:]
	if port == "" || strings.ContainsAny(port, "*%") {
		return ""
	}
	return port
}

// isUnder reports whether path is worktree or sits inside it. The client dev
// server runs in <worktree>/client, so a prefix match is the right test -- but a
// component-wise one, or /repo would match /repo-backup.
func isUnder(path, worktree string) bool {
	path = filepath.Clean(path)
	worktree = filepath.Clean(worktree)
	if path == worktree {
		return true
	}
	rel, err := filepath.Rel(worktree, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// shorten trims a path to its last two components for display.
func shorten(p string) string {
	parts := strings.Split(filepath.Clean(p), string(filepath.Separator))
	if len(parts) <= 2 {
		return p
	}
	return ".../" + filepath.Join(parts[len(parts)-2:]...)
}
