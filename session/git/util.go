package git

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// sanitizeBranchName transforms an arbitrary string into a Git branch name friendly string.
// Note: Git branch names have several rules, so this function uses a simple approach
// by allowing only a safe subset of characters.
//
// preserveCase keeps the caller's capitalisation. Git refs are case-sensitive, so
// "TASK-1234" is a perfectly legal branch; the default lower-cases anyway because
// git stores refs as files, and a case-insensitive filesystem cannot hold both
// "TASK-Foo" and "TASK-foo" at once.
func sanitizeBranchName(s string, preserveCase bool) string {
	if !preserveCase {
		s = strings.ToLower(s)
	}

	// Replace spaces with a dash
	s = strings.ReplaceAll(s, " ", "-")

	// Remove any characters not allowed in our safe subset.
	// Here we allow: letters, digits, dash, underscore, slash, and dot.
	re := regexp.MustCompile(`[^a-zA-Z0-9\-_/.]+`)
	s = re.ReplaceAllString(s, "")

	// Replace multiple dashes with a single dash (optional cleanup)
	reDash := regexp.MustCompile(`-+`)
	s = reDash.ReplaceAllString(s, "-")

	// Trim leading and trailing dashes or slashes to avoid issues
	s = strings.Trim(s, "-/")

	return s
}

// checkGHCLI checks if GitHub CLI is installed and configured
func checkGHCLI() error {
	// Check if gh is installed
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("GitHub CLI (gh) is not installed. Please install it first")
	}

	// Check if gh is authenticated
	cmd := exec.Command("gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("GitHub CLI is not configured. Please run 'gh auth login' first")
	}

	return nil
}

// IsGitRepo checks if the given path is within a git repository
func IsGitRepo(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel")
	return cmd.Run() == nil
}

// CurrentBranch returns the branch checked out at the given path. This can differ
// from the branch a session recorded when it was created: renaming the branch, or
// checking out a different one, from inside the worktree is common, and the
// recorded name then refers to something that may not exist any more.
//
// Returns an error on a detached HEAD, where there is no branch to name.
// HeadCommitTime returns the commit time of a worktree's HEAD.
//
// Read locally rather than from the pull request, because the alternative --
// asking gh for the PR's commits -- more than doubles the size of the response
// for a branch of any length, for one timestamp.
func HeadCommitTime(path string) (time.Time, error) {
	cmd := exec.Command("git", "-C", path, "log", "-1", "--format=%cI")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to read HEAD commit time at %s: %w", path, err)
	}
	when, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse HEAD commit time at %s: %w", path, err)
	}
	return when, nil
}

func CurrentBranch(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to read current branch at %s: %w", path, err)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return "", fmt.Errorf("detached HEAD at %s", path)
	}
	return branch, nil
}

func findGitRepoRoot(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to find Git repository root from path: %s", path)
	}
	return strings.TrimSpace(string(out)), nil
}
