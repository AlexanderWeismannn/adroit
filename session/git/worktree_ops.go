package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/log"
)

// Setup creates a new worktree for the session
func (g *GitWorktree) Setup() error { return g.setup(false) }

// SetupForResume re-adds the worktree of a paused session from its branch.
//
// Not Setup: Setup takes a branch it finds already existing as someone else's
// and stops owning it, so every pause and resume turned a branch Adroit created
// into one a kill would leave behind.
func (g *GitWorktree) SetupForResume() error { return g.setup(true) }

func (g *GitWorktree) setup(resume bool) error {
	// Ensure worktrees directory exists early (can be done in parallel with branch check)
	worktreesDir, err := getWorktreeDirectory()
	if err != nil {
		return fmt.Errorf("failed to get worktree directory: %w", err)
	}

	if err := os.MkdirAll(worktreesDir, 0755); err != nil {
		return err
	}

	// If this worktree uses a pre-existing branch, always set up from that branch
	// (it may exist locally or only on the remote).
	if g.isExistingBranch || resume {
		return g.setupFromExistingBranch()
	}

	// Check if branch exists using git CLI (much faster than go-git PlainOpen)
	_, err = g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/heads/%s", g.branchName))
	if err == nil {
		// The typed name landed on a branch that already exists, so it is not
		// ours to delete: without this, killing the session ran `git branch -D`
		// on it, unpushed commits and all.
		g.isExistingBranch = true
		return g.setupFromExistingBranch()
	}
	return g.setupNewWorktree()
}

// worktreeHoldingBranch returns the path of the worktree that currently has
// branchName checked out, or "" when no worktree does.
func (g *GitWorktree) worktreeHoldingBranch() (string, error) {
	output, err := g.runGitCommand(g.repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}

	var path string
	for _, line := range strings.Split(output, "\n") {
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			path = strings.TrimSpace(rest)
			continue
		}
		if rest, ok := strings.CutPrefix(line, "branch "); ok {
			if strings.TrimSpace(rest) == "refs/heads/"+g.branchName {
				return path, nil
			}
		}
	}
	return "", nil
}

// legacyConfigDirName is where worktrees lived before the fork was renamed; a
// worktree stranded there is exactly the case adoption exists for.
const legacyConfigDirName = ".claude-squad"

// adoptable says whether a worktree holding the branch may become this
// session's. Adopting hands the directory to this session's lifecycle: pausing
// commits whatever is uncommitted in it, and killing runs `git worktree remove
// -f` on it. So only a worktree Adroit itself minted -- under its worktrees
// directory, or the pre-rename one -- and that no stored session still owns.
// The repository's own checkout, or a worktree the user made by hand, is never
// taken over.
func adoptable(held string) error {
	var roots []string
	if dir, err := getWorktreeDirectory(); err == nil {
		roots = append(roots, dir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, legacyConfigDirName, "worktrees"))
	}
	minted := false
	for _, root := range roots {
		if isWithin(held, root) {
			minted = true
			break
		}
	}
	if !minted {
		return fmt.Errorf("that checkout is not one Adroit created, so it will not be taken over; " +
			"switch it to another branch first, or start a no-branch session there")
	}

	owned, err := config.StoredWorktreePaths()
	if err != nil {
		return fmt.Errorf("cannot tell whether another session owns it: %w", err)
	}
	for _, path := range owned {
		if samePath(path, held) {
			return fmt.Errorf("another session is using that worktree")
		}
	}
	return nil
}

// isWithin reports whether path is root or lies below it.
func isWithin(path, root string) bool {
	rel, err := filepath.Rel(canonicalPath(root), canonicalPath(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func samePath(a, b string) bool { return canonicalPath(a) == canonicalPath(b) }

// canonicalPath resolves symlinks where it can: git reports real paths, while
// stored paths are built from the home directory as given.
func canonicalPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

// setupFromExistingBranch creates a worktree from an existing branch
func (g *GitWorktree) setupFromExistingBranch() error {
	// Directory already created in Setup(), skip duplicate creation

	// git allows exactly one worktree per branch, so a branch already checked out
	// somewhere else can never be added again — the session would be permanently
	// unstartable. That happens whenever the worktree outlives the state entry that
	// named it: a config directory that moved, a state file reset, a hand-edited path.
	// Adopt the existing worktree instead; it is where the work actually is.
	if held, err := g.worktreeHoldingBranch(); err == nil && held != "" && !samePath(held, g.worktreePath) {
		if _, statErr := os.Stat(held); statErr == nil {
			if err := adoptable(held); err != nil {
				return fmt.Errorf("branch %s is already checked out at %s: %w", g.branchName, held, err)
			}
			log.InfoLog.Printf("branch %s is already checked out at %s; adopting that worktree",
				g.branchName, held)
			g.worktreePath = held
			g.adopted = true
			return g.recordBaseCommit()
		}
		// Registered but gone from disk: drop the registration so the add below works.
		_, _ = g.runGitCommand(g.repoPath, "worktree", "remove", "-f", held)
		_, _ = g.runGitCommand(g.repoPath, "worktree", "prune")
	}

	// Clean up any existing worktree first
	_, _ = g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath) // Ignore error if worktree doesn't exist
	// If the directory is still there (orphaned, not registered with git), drop it so `git worktree add` won't fail.
	_ = os.RemoveAll(g.worktreePath)

	// Check if the local branch exists
	_, localErr := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/heads/%s", g.branchName))
	if localErr != nil {
		// Local branch doesn't exist — check if remote tracking branch exists
		_, remoteErr := g.runGitCommand(g.repoPath, "show-ref", "--verify", fmt.Sprintf("refs/remotes/origin/%s", g.branchName))
		if remoteErr != nil {
			return fmt.Errorf("branch %s not found locally or on remote", g.branchName)
		}
		// Create a local tracking branch via worktree add -b
		if _, err := g.runGitCommand(g.repoPath, "worktree", "add", "-b", g.branchName, g.worktreePath, fmt.Sprintf("origin/%s", g.branchName)); err != nil {
			return fmt.Errorf("failed to create worktree from remote branch %s: %w", g.branchName, err)
		}
		return g.recordBaseCommit()
	}

	// Create a new worktree from the existing local branch
	if _, err := g.runGitCommand(g.repoPath, "worktree", "add", g.worktreePath, g.branchName); err != nil {
		return fmt.Errorf("failed to create worktree from branch %s: %w", g.branchName, err)
	}

	return g.recordBaseCommit()
}

// recordBaseCommit stores the commit the session starts from. Diffs are computed
// against it, so leaving it unset makes every git diff invocation fail with an
// ambiguous argument error once the session is running.
func (g *GitWorktree) recordBaseCommit() error {
	output, err := g.runGitCommand(g.worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("failed to get base commit hash for branch %s: %w", g.branchName, err)
	}
	g.baseCommitSHA = strings.TrimSpace(output)
	g.recordBaseBranch()
	return nil
}

// recordBaseBranch stores the name of the branch the repository was on when the
// session was created. The name is what survives the branch moving: the commit
// alone cannot tell resolveDiffBase whether master has since advanced past it.
// A detached checkout has no name to record, and leaves this empty.
func (g *GitWorktree) recordBaseBranch() {
	output, err := g.runGitCommand(g.repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return
	}
	name := strings.TrimSpace(output)
	if name == "" || name == "HEAD" {
		return
	}
	g.baseBranch = name
}

// setupNewWorktree creates a new worktree from HEAD
func (g *GitWorktree) setupNewWorktree() error {
	// Clean up any existing worktree first
	_, _ = g.runGitCommand(g.repoPath, "worktree", "remove", "-f", g.worktreePath) // Ignore error if worktree doesn't exist
	// If the directory is still there (orphaned, not registered with git), drop it so `git worktree add` won't fail.
	_ = os.RemoveAll(g.worktreePath)

	// Clean up any existing branch using git CLI (much faster than go-git PlainOpen)
	_, _ = g.runGitCommand(g.repoPath, "branch", "-D", g.branchName) // Ignore error if branch doesn't exist

	// Before HEAD is read, not after: HEAD is the commit this branch is cut
	// from, and the point of the sync is that it should be the current tip of the
	// default branch rather than wherever the checkout was last left.
	base := g.syncBaseBranch()
	if base == "" {
		base = "HEAD"
	}

	output, err := g.runGitCommand(g.repoPath, "rev-parse", base)
	if err != nil {
		if strings.Contains(err.Error(), "fatal: ambiguous argument 'HEAD'") ||
			strings.Contains(err.Error(), "fatal: not a valid object name") ||
			strings.Contains(err.Error(), "fatal: HEAD: not a valid object name") {
			return fmt.Errorf("this appears to be a brand new repository: please create an initial commit before creating an instance")
		}
		return fmt.Errorf("failed to get HEAD commit hash: %w", err)
	}
	headCommit := strings.TrimSpace(string(output))
	g.baseCommitSHA = headCommit
	g.recordBaseBranch()

	// Create a new worktree from the HEAD commit
	// Otherwise, we'll inherit uncommitted changes from the previous worktree.
	// This way, we can start the worktree with a clean slate.
	// TODO: we might want to give an option to use main/master instead of the current branch.
	if _, err := g.runGitCommand(g.repoPath, "worktree", "add", "-b", g.branchName, g.worktreePath, headCommit); err != nil {
		return fmt.Errorf("failed to create worktree from commit %s: %w", headCommit, err)
	}

	return nil
}

// Cleanup removes the worktree and associated branch
// AbandonSetup undoes a Setup whose session then failed to start.
//
// It is Cleanup for a worktree this Setup created. For one it adopted it does
// nothing: Cleanup runs `worktree remove -f`, which would throw away work that
// was sitting in that directory before this session ever existed -- the very
// work adoption exists to keep.
func (g *GitWorktree) AbandonSetup() error {
	if g.adopted {
		return nil
	}
	return g.Cleanup()
}

func (g *GitWorktree) Cleanup() error {
	var errs []error

	// Check if worktree path exists before attempting removal
	if _, err := os.Stat(g.worktreePath); err == nil {
		// Remove the worktree using git command
		if _, err := g.runGitCommandTimeout(g.repoPath, TeardownTimeout, "worktree", "remove", "-f", g.worktreePath); err != nil {
			errs = append(errs, err)
		}
	} else if !os.IsNotExist(err) {
		// Only append error if it's not a "not exists" error
		errs = append(errs, fmt.Errorf("failed to check worktree path: %w", err))
	}

	// Delete the branch using git CLI, but skip if this is a pre-existing branch
	if !g.isExistingBranch {
		if _, err := g.runGitCommandTimeout(g.repoPath, TeardownTimeout, "branch", "-D", g.branchName); err != nil {
			// Only log if it's not a "branch not found" error
			if !strings.Contains(err.Error(), "not found") {
				errs = append(errs, fmt.Errorf("failed to remove branch %s: %w", g.branchName, err))
			}
		}
	}

	// Prune the worktree to clean up any remaining references
	if err := g.Prune(); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return g.combineErrors(errs)
	}

	return nil
}

// Remove removes the worktree but keeps the branch
func (g *GitWorktree) Remove() error {
	// Remove the worktree using git command
	if _, err := g.runGitCommandTimeout(g.repoPath, TeardownTimeout, "worktree", "remove", "-f", g.worktreePath); err != nil {
		return fmt.Errorf("failed to remove worktree: %w", err)
	}

	return nil
}

// Prune removes all working tree administrative files and directories
func (g *GitWorktree) Prune() error {
	if _, err := g.runGitCommandTimeout(g.repoPath, TeardownTimeout, "worktree", "prune"); err != nil {
		return fmt.Errorf("failed to prune worktrees: %w", err)
	}
	return nil
}

// ManagedWorktree is what a stored session records about its worktree: enough
// for a reset to remove exactly that worktree, from the repository it belongs
// to, and its branch only when Adroit created the branch.
type ManagedWorktree struct {
	RepoPath         string
	WorktreePath     string
	BranchName       string
	IsExistingBranch bool
}

// CleanupWorktrees removes the worktrees Adroit made for the given sessions, and
// whatever else is left in its worktree directory. It is what `adroit reset`
// does to git.
//
// It used to list the worktrees of the repository it was run from, match them
// to the folders in the worktree directory with strings.Contains, and run
// branch -D on each match while its worktree still existed -- which git refuses
// for a checked-out branch, so Adroit's branches survived every reset, and the
// one guard against deleting a branch you already had was git's. The worktrees
// of every other repository were deleted as folders, their registrations and
// branches left behind. Now each stored session is torn down against its own
// repository, a branch goes only when Adroit created it, and a worktree outside
// the directory -- one adopted from where you had it -- is left alone. A
// leftover folder no session owns is removed as a folder: what it was a
// worktree of is pruned, and no branch is touched.
func CleanupWorktrees(owned []ManagedWorktree) error {
	worktreesDir, err := getWorktreeDirectory()
	if err != nil {
		return fmt.Errorf("failed to get worktree directory: %w", err)
	}
	return cleanupWorktrees(worktreesDir, owned)
}

func cleanupWorktrees(worktreesDir string, owned []ManagedWorktree) error {
	var errs []error
	pruned := map[string]bool{}

	for _, w := range owned {
		if w.RepoPath == "" || w.WorktreePath == "" || !isWithin(w.WorktreePath, worktreesDir) ||
			samePath(w.WorktreePath, worktreesDir) {
			continue
		}
		g := &GitWorktree{
			repoPath:         w.RepoPath,
			worktreePath:     w.WorktreePath,
			branchName:       w.BranchName,
			isExistingBranch: w.IsExistingBranch,
		}
		// Cleanup prunes as it goes.
		if err := g.Cleanup(); err != nil {
			errs = append(errs, err)
		}
		pruned[w.RepoPath] = true
	}

	entries, err := os.ReadDir(worktreesDir)
	if err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Errorf("failed to read worktree directory: %w", err))
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(worktreesDir, entry.Name())
		repo := repoOfWorktree(path)
		if err := os.RemoveAll(path); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove %s: %w", path, err))
			continue
		}
		if repo != "" && !pruned[repo] {
			pruned[repo] = true
			g := &GitWorktree{repoPath: repo}
			if err := g.Prune(); err != nil {
				log.WarningLog.Printf("could not prune the worktrees of %s: %v", repo, err)
			}
		}
	}

	return errors.Join(errs...)
}

// repoOfWorktree is the repository a linked worktree belongs to, read from the
// .git file git leaves in it ("gitdir: <repo>/.git/worktrees/<name>"), or ""
// when the folder is not one.
func repoOfWorktree(path string) string {
	data, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		return ""
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return ""
	}
	worktrees := filepath.Dir(filepath.Clean(gitdir))
	if filepath.Base(worktrees) != "worktrees" || filepath.Base(filepath.Dir(worktrees)) != ".git" {
		return ""
	}
	return filepath.Dir(filepath.Dir(worktrees))
}
