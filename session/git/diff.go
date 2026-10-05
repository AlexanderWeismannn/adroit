package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// diffBaseMaxAge is how long a resolved diff base is trusted before it is
// recomputed. Resolving costs a handful of `git merge-base` invocations, and the
// answer only moves when the branch is rebased or the base branch is fetched --
// both far rarer than the 4s tick that asks for the counts.
const diffBaseMaxAge = 30 * time.Second

// DiffStats holds statistics about the changes in a diff
type DiffStats struct {
	// Content is the full diff content
	Content string
	// Added is the number of added lines
	Added int
	// Removed is the number of removed lines
	Removed int
	// Error holds any error that occurred during diff computation
	// This allows propagating setup errors (like missing base commit) without breaking the flow
	Error error
}

func (d *DiffStats) IsEmpty() bool {
	return d.Added == 0 && d.Removed == 0 && d.Content == ""
}

// privateIndexName is Adroit's own index, beside the worktree's real one in its
// git directory, so `git worktree remove` takes it away with everything else.
const privateIndexName = "adroit-index"

// privateIndexPath is where that index lives for this worktree.
// Cached, since it cannot change for the life of the worktree and every diff
// read on every tick needs it.
func (g *GitWorktree) privateIndexPath() (string, error) {
	g.indexMu.Lock()
	defer g.indexMu.Unlock()
	if g.privateIndex != "" {
		return g.privateIndex, nil
	}
	out, err := g.runGitCommand(g.worktreePath, "rev-parse", "--git-path", privateIndexName)
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(g.worktreePath, p)
	}
	g.privateIndex = p
	return p, nil
}

// stageUntrackedPrivately folds untracked files into the diff without touching
// the index the agent works with.
//
// Counting untracked files takes `git add -N`, which records intent-to-add
// entries. Run against the real index, that changed the agent's repository every
// fifteen seconds behind its back: `git status` listed every stray file as a new
// file, `git commit -a` committed them, and the add took index.lock against the
// agent's own git commands. So the real index is copied to a private one and the
// add runs there; the diff reads use the copy.
func (g *GitWorktree) stageUntrackedPrivately() error {
	private, err := g.privateIndexPath()
	if err != nil {
		return err
	}
	real, err := g.runGitCommand(g.worktreePath, "rev-parse", "--git-path", "index")
	if err != nil {
		return err
	}
	realPath := strings.TrimSpace(real)
	if !filepath.IsAbs(realPath) {
		realPath = filepath.Join(g.worktreePath, realPath)
	}
	data, err := os.ReadFile(realPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not read the index: %w", err)
	}
	tmp := private + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("could not copy the index: %w", err)
	}
	if err := os.Rename(tmp, private); err != nil {
		return fmt.Errorf("could not copy the index: %w", err)
	}
	_, err = g.runGitCommandEnv(g.worktreePath, gitCommandTimeout, []string{"GIT_INDEX_FILE=" + private},
		"add", "-N", ".")
	return err
}

// diffEnv points a diff read at the private index once one exists; before the
// first staging pass the real index is all there is.
func (g *GitWorktree) diffEnv() []string {
	private, err := g.privateIndexPath()
	if err != nil {
		return nil
	}
	if _, err := os.Stat(private); err != nil {
		return nil
	}
	return []string{"GIT_INDEX_FILE=" + private}
}

// Diff returns the git diff between the worktree and the base branch along with statistics
func (g *GitWorktree) Diff() *DiffStats {
	stats := &DiffStats{}

	if err := g.stageUntrackedPrivately(); err != nil {
		stats.Error = err
		return stats
	}

	base, err := g.resolveDiffBase()
	if err != nil {
		stats.Error = err
		return stats
	}

	content, err := g.runGitCommandEnv(g.worktreePath, gitCommandTimeout, g.diffEnv(), "--no-pager", "diff", base)
	if err != nil {
		stats.Error = err
		return stats
	}
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			stats.Added++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			stats.Removed++
		}
	}
	stats.Content = content

	return stats
}

// DiffNumstat returns the added/removed line counts between the worktree and the
// base branch without loading the full diff content into memory. Use this when
// only the summary counts are needed (e.g. for unselected instances in the list).
//
// stageUntracked controls whether untracked files are counted, which costs an
// `add -N` walk of the whole worktree: measured at 12.1ms against the 4-11ms of
// the diff it feeds, making it the more expensive half of the cheap path. A new
// untracked file is a rare event next to the rate this runs at, so the caller
// asks for it on a slower clock than the counts themselves -- see
// untrackedMaxAge.
func (g *GitWorktree) DiffNumstat(stageUntracked bool) *DiffStats {
	stats := &DiffStats{}

	if stageUntracked {
		if err := g.stageUntrackedPrivately(); err != nil {
			stats.Error = err
			return stats
		}
	}

	base, err := g.resolveDiffBase()
	if err != nil {
		stats.Error = err
		return stats
	}

	out, err := g.runGitCommandEnv(g.worktreePath, gitCommandTimeout, g.diffEnv(), "--no-pager", "diff", "--numstat", base)
	if err != nil {
		stats.Error = err
		return stats
	}

	stats.Added, stats.Removed = parseNumstat(out)
	return stats
}

// parseNumstat sums the added/removed columns from `git diff --numstat` output.
// Each line is formatted as <added>\t<removed>\t<path>. Binary files report
// "-\t-\t<path>" and are ignored for line totals.
func parseNumstat(out string) (added int, removed int) {
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 {
			continue
		}
		a, aerr := strconv.Atoi(fields[0])
		r, rerr := strconv.Atoi(fields[1])
		if aerr != nil || rerr != nil {
			continue
		}
		added += a
		removed += r
	}
	return added, removed
}

// resolveDiffBase returns the commit a session's work should be measured from.
//
// The recorded baseCommitSHA is a snapshot of whatever the repository's checkout
// happened to point at when the session was created, and it is frozen there for
// the life of the session. Two ordinary things make it the wrong place to
// measure from, and both inflate the counts by everything that landed on the
// base branch in between:
//
//   - the repository's local base branch was already behind its remote at
//     creation, so the worktree started from a fork point newer than the SHA;
//   - the session rebased onto, or merged in, a newer base branch, folding
//     other people's commits into the range.
//
// Measured on this machine: a branch carrying 7 commits of its own reported
// +80350/-13203 because the local master it was diffed against was 47 merged
// PRs behind origin/master.
//
// So the base is derived instead: take the merge-base of the worktree against
// each plausible base ref, and keep the newest of those and the recorded SHA.
// "Newest" is what makes this safe -- the recorded SHA is always a candidate and
// every candidate is an ancestor of HEAD, so the resolved base can only move
// forward. This never counts more than the old behaviour did, only less.
func (g *GitWorktree) resolveDiffBase() (string, error) {
	g.baseMu.Lock()
	defer g.baseMu.Unlock()

	if g.resolvedBase != "" && time.Since(g.resolvedBaseAt) < diffBaseMaxAge {
		return g.resolvedBase, nil
	}

	best := strings.TrimSpace(g.baseCommitSHA)
	for _, ref := range g.baseRefCandidates() {
		out, err := g.runGitCommand(g.worktreePath, "merge-base", "HEAD", ref)
		if err != nil {
			continue
		}
		mb := strings.TrimSpace(out)
		if mb == "" {
			continue
		}
		if best == "" || g.isAncestor(best, mb) {
			best = mb
		}
	}

	if best == "" {
		// Kept verbatim: both the daemon and the list treat this phrasing as the
		// "not set up yet" case rather than something to warn about.
		return "", fmt.Errorf("base commit SHA not set")
	}

	g.resolvedBase = best
	g.resolvedBaseAt = time.Now()
	return best, nil
}

// baseRefCandidates lists the refs the session's fork point might be measured
// against, best guess first. The branch recorded at creation wins; the remote
// spelling of a branch is preferred over the local one, since a local base
// branch is exactly the thing that goes stale. Only refs that actually resolve
// are returned, so a repository with no `origin` costs nothing.
func (g *GitWorktree) baseRefCandidates() []string {
	ordered := make([]string, 0, 8)
	seen := make(map[string]bool, 8)
	add := func(ref string) {
		if ref == "" || seen[ref] {
			return
		}
		seen[ref] = true
		ordered = append(ordered, ref)
	}

	if g.baseBranch != "" {
		add("origin/" + g.baseBranch)
		add(g.baseBranch)
	}
	// The repository's own idea of its default branch, when origin/HEAD is set.
	if out, err := g.runGitCommand(g.worktreePath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		add(strings.TrimSpace(out))
	}
	for _, ref := range []string{"origin/main", "origin/master", "main", "master"} {
		add(ref)
	}

	live := ordered[:0]
	for _, ref := range ordered {
		if _, err := g.runGitCommand(g.worktreePath, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil {
			live = append(live, ref)
		}
	}
	return live
}

// isAncestor reports whether older is an ancestor of newer. `merge-base
// --is-ancestor` exits non-zero for "no", which runGitCommand surfaces as an
// error, so a genuine failure (a missing object, say) is indistinguishable from
// a false -- and answering "no" is the conservative reading either way: it
// leaves the base where it is rather than moving it forward on a bad answer.
func (g *GitWorktree) isAncestor(older, newer string) bool {
	if older == newer {
		return false
	}
	_, err := g.runGitCommand(g.worktreePath, "merge-base", "--is-ancestor", older, newer)
	return err == nil
}
