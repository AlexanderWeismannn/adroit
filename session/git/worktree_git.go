package git

import (
	"context"
	"fmt"
	"github.com/AlexanderWeismannn/adroit/cmd"
	"github.com/AlexanderWeismannn/adroit/log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// MaxBranchSearchResults is the maximum number of branches returned by SearchBranches.
const MaxBranchSearchResults = 50

// FetchBranches fetches and prunes remote-tracking branches (best-effort, won't fail if offline).
func FetchBranches(repoPath string) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	_ = cmd.NonInteractiveGit(exec.CommandContext(ctx, "git", "-C", repoPath, "fetch", "--prune")).Run()
}

// SearchBranches searches for branches whose name contains filter (case-insensitive),
// ordered by most recently updated first. Returns at most MaxBranchSearchResults.
// If filter is empty, returns all branches up to the limit.
func SearchBranches(repoPath, filter string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoPath, "branch", "-a",
		"--sort=-committerdate",
		"--format=%(refname:short)")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %s (%w)", output, err)
	}

	seen := make(map[string]bool)
	var branches []string
	lower := strings.ToLower(filter)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "HEAD") {
			continue
		}
		name := strings.TrimPrefix(line, "origin/")
		if seen[name] {
			continue
		}
		seen[name] = true
		if filter != "" && !strings.Contains(strings.ToLower(name), lower) {
			continue
		}
		branches = append(branches, name)
		if len(branches) >= MaxBranchSearchResults {
			break
		}
	}
	return branches, nil
}

// TeardownTimeout bounds the git commands that remove a session's worktree and
// branch.
//
// The rest of the git in this package is deliberately unbounded: a commit runs
// the repository's hooks, and prettier and eslint over a large staged set take
// as long as they take. Teardown is the opposite -- no hooks, no network, a few
// index writes -- so it is either quick or it is blocked on a lock another git
// process is holding, and on this machine that process is as likely to be
// GitHub Desktop on the Windows side as anything we started. Waiting one out
// forever is what left a kill with nothing to do but close the terminal.
const TeardownTimeout = 20 * time.Second

// runGitCommandTimeout is runGitCommand with a ceiling, reported as a timeout
// rather than as whatever git happened to leave on stderr when it was killed.
func (g *GitWorktree) runGitCommandTimeout(path string, timeout time.Duration, args ...string) (string, error) {
	return g.runGitCommandEnv(path, timeout, nil, args...)
}

// runGitCommandEnv is runGitCommandTimeout with extra environment, for the diff
// reads that point git at Adroit's own index.
func (g *GitWorktree) runGitCommandEnv(path string, timeout time.Duration, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	baseArgs := []string{"-C", path}
	c := cmd.NonInteractiveGit(exec.CommandContext(ctx, "git", append(baseArgs, args...)...))
	c.Env = append(c.Env, env...)

	output, err := c.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("git %s timed out after %s (another git process may be holding the index lock)",
			strings.Join(args, " "), timeout)
	}
	if err != nil {
		return "", fmt.Errorf("git command failed: %s (%w)", output, err)
	}

	return string(output), nil
}

// gitCommandTimeout is the ceiling on any git command Adroit runs. Generous,
// because it covers `worktree add` of a large repository; the point is that there
// IS one. The diff tick waits on every session's git before it can report any of
// them, so a single hung call used to freeze every row's status indefinitely.
const gitCommandTimeout = 2 * time.Minute

// runGitCommand executes a git command and returns any error
func (g *GitWorktree) runGitCommand(path string, args ...string) (string, error) {
	return g.runGitCommandTimeout(path, gitCommandTimeout, args...)
}

// PushChanges commits and pushes changes in the worktree to the remote branch
func (g *GitWorktree) PushChanges(commitMessage string, open bool) error {
	if err := checkGHCLI(); err != nil {
		return err
	}

	// Check if there are any changes to commit
	isDirty, err := g.IsDirty()
	if err != nil {
		return fmt.Errorf("failed to check for changes: %w", err)
	}

	if isDirty {
		// Stage all changes
		if _, err := g.runGitCommand(g.worktreePath, "add", "."); err != nil {
			log.ErrorLog.Print(err)
			return fmt.Errorf("failed to stage changes: %w", err)
		}

		// Create commit
		if _, err := g.runGitCommand(g.worktreePath, "commit", "-m", commitMessage, "--no-verify"); err != nil {
			log.ErrorLog.Print(err)
			return fmt.Errorf("failed to commit changes: %w", err)
		}
	}

	// First push the branch to remote to ensure it exists
	pushCmd := exec.Command("gh", "repo", "sync", "--source", "-b", g.branchName)
	pushCmd.Dir = g.worktreePath
	if err := pushCmd.Run(); err != nil {
		// If sync fails, try creating the branch on remote first
		gitPushCmd := exec.Command("git", "push", "-u", "origin", g.branchName)
		gitPushCmd.Dir = g.worktreePath
		if pushOutput, pushErr := gitPushCmd.CombinedOutput(); pushErr != nil {
			log.ErrorLog.Print(pushErr)
			return fmt.Errorf("failed to push branch: %s (%w)", pushOutput, pushErr)
		}
	}

	// Now sync with remote
	syncCmd := exec.Command("gh", "repo", "sync", "-b", g.branchName)
	syncCmd.Dir = g.worktreePath
	if output, err := syncCmd.CombinedOutput(); err != nil {
		log.ErrorLog.Print(err)
		return fmt.Errorf("failed to sync changes: %s (%w)", output, err)
	}

	// Open the branch in the browser
	if open {
		if err := g.OpenBranchURL(); err != nil {
			// Just log the error but don't fail the push operation
			log.ErrorLog.Printf("failed to open branch URL: %v", err)
		}
	}

	return nil
}

// CommitChanges commits changes locally without pushing to remote
func (g *GitWorktree) CommitChanges(commitMessage string) error {
	// Check if there are any changes to commit
	isDirty, err := g.IsDirty()
	if err != nil {
		return fmt.Errorf("failed to check for changes: %w", err)
	}

	if isDirty {
		// Stage all changes
		if _, err := g.runGitCommand(g.worktreePath, "add", "."); err != nil {
			log.ErrorLog.Print(err)
			return fmt.Errorf("failed to stage changes: %w", err)
		}

		// Create commit (local only)
		if _, err := g.runGitCommand(g.worktreePath, "commit", "-m", commitMessage, "--no-verify"); err != nil {
			log.ErrorLog.Print(err)
			return fmt.Errorf("failed to commit changes: %w", err)
		}
	}

	return nil
}

// IsDirty checks if the worktree has uncommitted changes
func (g *GitWorktree) IsDirty() (bool, error) {
	output, err := g.runGitCommand(g.worktreePath, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("failed to check worktree status: %w", err)
	}
	return len(output) > 0, nil
}

// IsValidWorktree reports whether the worktree path exists and contains a
// .git entry, i.e. git can still recognize it as a working tree.
// Returns (false, nil) if the worktree is orphaned (path or .git missing).
func (g *GitWorktree) IsValidWorktree() (bool, error) {
	if _, err := os.Stat(g.worktreePath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to stat worktree path: %w", err)
	}
	if _, err := os.Stat(filepath.Join(g.worktreePath, ".git")); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to stat worktree .git: %w", err)
	}
	return true, nil
}

// IsBranchCheckedOut checks if the instance branch is currently checked out
func (g *GitWorktree) IsBranchCheckedOut() (bool, error) {
	output, err := g.runGitCommand(g.repoPath, "branch", "--show-current")
	if err != nil {
		return false, fmt.Errorf("failed to get current branch: %w", err)
	}
	return strings.TrimSpace(string(output)) == g.branchName, nil
}

// OpenBranchURL opens the branch URL in the default browser
func (g *GitWorktree) OpenBranchURL() error {
	// Check if GitHub CLI is available
	if err := checkGHCLI(); err != nil {
		return err
	}

	cmd := exec.Command("gh", "browse", "--branch", g.branchName)
	cmd.Dir = g.worktreePath
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to open branch URL: %w", err)
	}
	return nil
}
