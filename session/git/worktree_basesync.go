package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/AlexanderWeismannn/adroit/cmd"
	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/log"
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
// local edit blocks it, the session is still cut from the upstream tip. (An
// untracked file in the way is stashed instead; see fastForwardPastUntracked.) Before that, one leftover untracked file in the main
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
	_, err = g.runGitCommand(g.repoPath, "merge", "--ff-only", remoteRef)
	if err != nil {
		err = g.fastForwardPastUntracked(branch, remoteRef, err)
	}
	if err != nil {
		log.InfoLog.Printf("could not fast-forward %s to %s (branching %s from %s anyway): %v", branch, remoteRef, g.branchName, remoteRef, err)
		return tip
	}
	log.InfoLog.Printf("fast-forwarded %s to %s before creating %s", branch, remoteRef, g.branchName)
	return tip
}

// baseSyncStashMessage labels the stash fastForwardPastUntracked leaves behind,
// so `git stash list` says where it came from.
const baseSyncStashMessage = "adroit: untracked files that blocked a fast-forward"

// fastForwardPastUntracked retries a fast-forward that mergeErr says was refused,
// after moving the untracked files that the upstream adds into a stash.
//
// Those files are the usual reason a fast-forward is refused, and a stubborn
// one: a draft that was later committed upstream in finished form -- a
// scaffolded migration, say -- blocks every fast-forward from then on, and
// nothing prompts anyone to clean it up. One such file kept a main checkout 559
// commits behind. The stash keeps them, so nothing is lost: `git stash pop`
// puts them back.
//
// Only the blocking files are stashed, and only untracked ones; an edit to a
// tracked file still refuses the fast-forward, as it did before. If the retry
// fails anyway, the stash is popped straight back, so a refusal leaves the
// checkout exactly as it was found.
func (g *GitWorktree) fastForwardPastUntracked(branch, remoteRef string, mergeErr error) error {
	blocking := g.untrackedFilesAdded(remoteRef)
	if len(blocking) == 0 {
		return mergeErr
	}

	args := []string{"stash", "push", "--include-untracked", "-m", baseSyncStashMessage, "--"}
	for _, path := range blocking {
		args = append(args, ":(literal)"+path)
	}
	if _, err := g.runGitCommand(g.repoPath, args...); err != nil {
		return fmt.Errorf("%w; stashing the untracked files in the way also failed: %v", mergeErr, err)
	}

	if _, err := g.runGitCommand(g.repoPath, "merge", "--ff-only", remoteRef); err != nil {
		if _, popErr := g.runGitCommand(g.repoPath, "stash", "pop"); popErr != nil {
			log.ErrorLog.Printf("could not put back %s after a refused fast-forward; they are in the stash %q: %v",
				strings.Join(blocking, ", "), baseSyncStashMessage, popErr)
		}
		return err
	}
	log.WarningLog.Printf("stashed untracked %s, which blocked fast-forwarding %s to %s; `git stash pop` in %s restores them",
		strings.Join(blocking, ", "), branch, remoteRef, g.repoPath)
	return nil
}

// untrackedFilesAdded lists the untracked, unignored files in the main checkout
// that a fast-forward to remoteRef would have to overwrite. Ignored files are
// left out because git overwrites those without asking.
func (g *GitWorktree) untrackedFilesAdded(remoteRef string) []string {
	untracked, err := g.runGitCommand(g.repoPath, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil
	}
	incoming, err := g.runGitCommand(g.repoPath, "diff", "-z", "--name-only", "--diff-filter=A", "HEAD", remoteRef)
	if err != nil {
		return nil
	}
	added := make(map[string]bool)
	for _, path := range strings.Split(incoming, "\x00") {
		if path != "" {
			added[path] = true
		}
	}
	var blocking []string
	for _, path := range strings.Split(untracked, "\x00") {
		if added[path] {
			blocking = append(blocking, path)
		}
	}
	return blocking
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
