package git

import (
	"fmt"
	"github.com/AlexanderWeismannn/adroit/config"
	"github.com/AlexanderWeismannn/adroit/log"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func getWorktreeDirectory() (string, error) {
	configDir, err := config.GetConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(configDir, "worktrees"), nil
}

// GitWorktree manages git worktree operations for a session
type GitWorktree struct {
	// Path to the repository
	repoPath string
	// Path to the worktree
	worktreePath string
	// Name of the session
	sessionName string
	// Branch name for the worktree
	branchName string
	// Base commit hash for the worktree
	baseCommitSHA string
	// baseBranch is the branch the session forked from, recorded by name rather
	// than by commit so the fork point can be re-derived after the branch is
	// rebased or the base branch moves on. Empty for a session created by an
	// older build, or from a detached checkout, in which case resolveDiffBase
	// falls back to the repository's default branch.
	baseBranch string
	// resolvedBase caches what resolveDiffBase last worked out, and baseMu
	// guards it: diffs are computed off the main event loop.
	resolvedBase   string
	resolvedBaseAt time.Time
	baseMu         sync.Mutex
	// privateIndex caches where Adroit's own index for this worktree lives; see
	// stageUntrackedPrivately.
	privateIndex string
	indexMu      sync.Mutex
	// isExistingBranch is true if the branch existed before the session was created.
	// When true, the branch will not be deleted on cleanup.
	isExistingBranch bool
	// adopted is true when Setup took over a worktree that was already holding
	// the branch instead of creating one. That directory, and whatever is
	// uncommitted in it, predates this session.
	adopted bool
}

func NewGitWorktreeFromStorage(repoPath string, worktreePath string, sessionName string, branchName string, baseCommitSHA string, baseBranch string, isExistingBranch bool) *GitWorktree {
	return &GitWorktree{
		repoPath:         repoPath,
		worktreePath:     worktreePath,
		sessionName:      sessionName,
		branchName:       branchName,
		baseCommitSHA:    baseCommitSHA,
		baseBranch:       baseBranch,
		isExistingBranch: isExistingBranch,
	}
}

// resolveWorktreePaths resolves the repo root and generates a unique worktree path for the given branch name.
// PreviewBranchName is the branch a session of this name would be created on.
//
// Exported so the interface can show it while the name is still being typed:
// the branch is derived from the name by rules -- a prefix, a sanitiser, a case
// policy -- that are invisible until the branch already exists, which is how
// "TASK-5635-UI" quietly became "TASK-TASK-5635-UI".
func PreviewBranchName(prefix string, preserveCase bool, sessionName string) string {
	if sessionName == "" {
		return ""
	}
	return sanitizeBranchName(applyBranchPrefix(prefix, sessionName), preserveCase)
}

// applyBranchPrefix prefixes a session name, unless it already carries it.
//
// Naming a session after the ticket is the obvious thing to do, and with a
// prefix configured that produced "TASK-TASK-5635-UI" -- a branch nobody meant,
// which then names the worktree directory and every push. Matched without regard
// to case, since a prefix typed either way means the same thing; the name itself
// keeps whatever case it was given.
func applyBranchPrefix(prefix, sessionName string) string {
	if prefix == "" || strings.HasPrefix(strings.ToLower(sessionName), strings.ToLower(prefix)) {
		return sessionName
	}
	return prefix + sessionName
}

// RepoRoot is the root of the repository containing path.
//
// Exported because the branch prefix and the case policy are per repository, and
// a caller with only a working directory in hand -- the naming overlay, which
// runs before any worktree exists -- has to resolve the root before it can ask
// the config anything.
func RepoRoot(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		log.ErrorLog.Printf("git worktree path abs error, falling back to repoPath %s: %s", path, err)
		absPath = path
	}
	return findGitRepoRoot(absPath)
}

func resolveWorktreePaths(repoPath string, branchName string, preserveCase bool) (resolvedRepo string, worktreePath string, err error) {
	resolvedRepo, err = RepoRoot(repoPath)
	if err != nil {
		return "", "", err
	}

	worktreeDir, err := getWorktreeDirectory()
	if err != nil {
		return "", "", err
	}

	worktreePath = filepath.Join(worktreeDir, sanitizeBranchName(branchName, preserveCase))
	worktreePath = worktreePath + "_" + fmt.Sprintf("%x", time.Now().UnixNano())

	return resolvedRepo, worktreePath, nil
}

// NewGitWorktree creates a new GitWorktree instance
func NewGitWorktree(repoPath string, sessionName string) (tree *GitWorktree, branchname string, err error) {
	cfg := config.LoadConfig()

	// The root is resolved before the name is derived, not after: the prefix and
	// the case policy are answers about a REPOSITORY, and repoPath at this point
	// is only some directory inside one.
	root, err := RepoRoot(repoPath)
	if err != nil {
		return nil, "", err
	}
	preserveCase := cfg.PreserveBranchCaseFor(root)

	branchName := applyBranchPrefix(cfg.BranchPrefixFor(root), sessionName)
	// Sanitize the final branch name to handle invalid characters from any source
	// (e.g., backslashes from Windows domain usernames like DOMAIN\user)
	branchName = sanitizeBranchName(branchName, preserveCase)

	repoPath, worktreePath, err := resolveWorktreePaths(root, branchName, preserveCase)
	if err != nil {
		return nil, "", err
	}

	return &GitWorktree{
		repoPath:     repoPath,
		sessionName:  sessionName,
		branchName:   branchName,
		worktreePath: worktreePath,
	}, branchName, nil
}

// NewGitWorktreeFromBranch creates a new GitWorktree that uses an existing branch.
// The branch will not be deleted on cleanup.
func NewGitWorktreeFromBranch(repoPath string, branchName string, sessionName string) (*GitWorktree, error) {
	// The branch already exists and is used verbatim; the flag only affects the
	// worktree directory name derived from it.
	cfg := config.LoadConfig()
	root, err := RepoRoot(repoPath)
	if err != nil {
		return nil, err
	}
	repoPath, worktreePath, err := resolveWorktreePaths(root, branchName, cfg.PreserveBranchCaseFor(root))
	if err != nil {
		return nil, err
	}

	return &GitWorktree{
		repoPath:         repoPath,
		sessionName:      sessionName,
		branchName:       branchName,
		worktreePath:     worktreePath,
		isExistingBranch: true,
	}, nil
}

// IsExistingBranch returns whether this worktree uses a pre-existing branch
func (g *GitWorktree) IsExistingBranch() bool {
	return g.isExistingBranch
}

// GetWorktreePath returns the path to the worktree
func (g *GitWorktree) GetWorktreePath() string {
	return g.worktreePath
}

// GetBranchName returns the name of the branch associated with this worktree
func (g *GitWorktree) GetBranchName() string {
	return g.branchName
}

// GetRepoPath returns the path to the repository
func (g *GitWorktree) GetRepoPath() string {
	return g.repoPath
}

// GetRepoName returns the name of the repository (last part of the repoPath).
func (g *GitWorktree) GetRepoName() string {
	return filepath.Base(g.repoPath)
}

// GetBaseCommitSHA returns the base commit SHA for the worktree
func (g *GitWorktree) GetBaseCommitSHA() string {
	return g.baseCommitSHA
}

// GetBaseBranch returns the branch the session forked from, if it was recorded.
func (g *GitWorktree) GetBaseBranch() string {
	return g.baseBranch
}
