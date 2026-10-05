package git

import (
	"context"
	"github.com/AlexanderWeismannn/adroit/cmd"
	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/log"
	"os/exec"
	"strings"
	"time"
)

// baseSyncTimeout bounds the fetch. A new session is being created and the user
// is waiting on it, so an unreachable remote has to give up rather than hang the
// setup: everything this does is an optimisation over branching from HEAD, and
// branching from HEAD is always still available.
const baseSyncTimeout = 20 * time.Second

// syncBaseBranch brings the repository's default branch up to date with its
// upstream before a new session's branch is cut from it.
//
// Without this a new branch inherits whatever the main checkout happened to be
// sitting on -- which is only ever as fresh as the last time someone pulled --
// so a session started on a master last updated a week ago begins a week behind,
// and nothing on screen says so. The cost surfaces later, as a rebase, a
// conflict, or a CI run against a base that has moved.
//
// Deliberately narrow in what it will touch:
//
//   - Only the DEFAULT branch. A checkout parked on a feature branch is parked
//     there on purpose, and moving it would be a surprise; a session cut from it
//     branches from where the user left it.
//   - Only a FAST-FORWARD. A default branch that has diverged from its remote
//     has local commits worth more than this convenience, and merging or
//     resetting it is not a decision to make silently.
//   - Never fatal. Offline, no remote, no upstream: each of these just leaves
//     HEAD where it is and the session starts from there, exactly as it did
//     before this existed.
//
// It returns the commit the new branch should be cut from, or "" for HEAD. The
// fast-forward of the main checkout is a courtesy, not a precondition: when a
// stray untracked file or a local edit blocks it, the session is still cut from
// the upstream tip. Before that, one leftover untracked file in the main
// checkout silently pinned every new session to a weeks-old base.
func (g *GitWorktree) syncBaseBranch() string {
	if !config.LoadConfig().SyncBaseBranchEnabled() {
		return ""
	}

	branch := g.currentBranch()
	if branch == "" {
		// Detached, or git could not say. Nothing to fast-forward.
		return ""
	}
	if def := g.defaultBranchName(); def == "" || def != branch {
		return ""
	}

	upstream, err := g.runGitCommand(g.repoPath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}")
	if err != nil {
		return ""
	}
	remoteRef := strings.TrimSpace(upstream)
	remote, ref, ok := strings.Cut(remoteRef, "/")
	if !ok {
		return ""
	}

	if err := g.fetchWithTimeout(remote, ref); err != nil {
		log.InfoLog.Printf("could not fetch %s before branching from %s: %v", remoteRef, branch, err)
		// Fall through: the remote-tracking ref may still be ahead of the local
		// branch from an earlier fetch, and fast-forwarding to that is strictly
		// better than not doing so.
	}

	local, err := g.runGitCommand(g.repoPath, "rev-parse", branch)
	if err != nil {
		return ""
	}
	target, err := g.runGitCommand(g.repoPath, "rev-parse", remoteRef)
	if err != nil {
		return ""
	}
	if strings.TrimSpace(local) == strings.TrimSpace(target) {
		return ""
	}

	// merge-base --is-ancestor exits non-zero for "no", which runGitCommand
	// reports as an error -- so a genuine failure and a real divergence are read
	// the same way, and both mean "leave the branch alone".
	if _, err := g.runGitCommand(g.repoPath, "merge-base", "--is-ancestor", branch, remoteRef); err != nil {
		log.InfoLog.Printf("%s has diverged from %s; branching from it as-is", branch, remoteRef)
		return ""
	}

	// --ff-only moves the ref and updates the files, and never writes a commit.
	// It refuses outright if an uncommitted change is in the way, leaving the
	// checkout untouched: the user's work in progress there outranks a tidy main
	// checkout. The session is cut from the upstream tip either way.
	tip := strings.TrimSpace(target)
	if _, err := g.runGitCommand(g.repoPath, "merge", "--ff-only", remoteRef); err != nil {
		log.InfoLog.Printf("could not fast-forward %s to %s (branching %s from %s anyway): %v", branch, remoteRef, g.branchName, remoteRef, err)
		return tip
	}
	log.InfoLog.Printf("fast-forwarded %s to %s before creating %s", branch, remoteRef, g.branchName)
	return tip
}

// currentBranch returns the checked-out branch of the main repository, or "" if
// it is detached.
func (g *GitWorktree) currentBranch() string {
	out, err := g.runGitCommand(g.repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(out)
	if name == "HEAD" {
		return ""
	}
	return name
}

// defaultBranchName is the repository's own idea of its default branch, taken
// from origin/HEAD. A repository whose origin/HEAD was never set (a clone made
// with --no-checkout, a remote added by hand) falls back to whichever of main or
// master exists locally.
func (g *GitWorktree) defaultBranchName() string {
	if out, err := g.runGitCommand(g.repoPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); name != "" {
			return name
		}
	}
	for _, name := range []string{"main", "master"} {
		if _, err := g.runGitCommand(g.repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+name); err == nil {
			return name
		}
	}
	return ""
}

// fetchWithTimeout updates one remote-tracking ref, giving up after
// baseSyncTimeout. Not routed through runGitCommand because that has no way to
// cancel, and this is the one call here that touches the network.
func (g *GitWorktree) fetchWithTimeout(remote, ref string) error {
	ctx, cancel := context.WithTimeout(context.Background(), baseSyncTimeout)
	defer cancel()

	fetch := cmd.NonInteractiveGit(exec.CommandContext(ctx, "git", "-C", g.repoPath, "fetch", "--quiet", remote, ref))
	if out, err := fetch.CombinedOutput(); err != nil {
		return combineFetchError(out, err)
	}
	return nil
}

func combineFetchError(out []byte, err error) error {
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return &fetchError{msg: msg, err: err}
	}
	return err
}

type fetchError struct {
	msg string
	err error
}

func (e *fetchError) Error() string { return e.msg }
func (e *fetchError) Unwrap() error { return e.err }
