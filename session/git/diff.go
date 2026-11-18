package git

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// diffBaseMaxAge is how long a resolved diff base is trusted when nothing it
// was worked out from has moved. The base is keyed on HEAD and the candidate
// refs (see baseKey), which catches every rebase, merge and fetch; this ceiling
// is only for what the key cannot see, like origin/HEAD being repointed at a
// branch on the same commit.
const diffBaseMaxAge = 10 * time.Minute

// diffState is what the diff path remembers between ticks, guarded by mu:
// diffs are computed off the main event loop.
type diffState struct {
	mu sync.Mutex
	// paths is where this worktree keeps its indexes and HEAD, resolved once:
	// none of it can move for the life of the worktree.
	paths *worktreePaths
	// resolvedBase is what resolveDiffBase last worked out, baseKey the state
	// of HEAD and the candidate refs it was worked out under, and resolvedBaseAt
	// when.
	resolvedBase   string
	baseKey        string
	resolvedBaseAt time.Time
	// staged says the private index holds a staging of stagedIndex -- the real
	// index as it stood -- plus the untracked files whose list hashes to
	// stagedUntracked. Both unchanged, staging again would write the same file.
	staged          bool
	stagedIndex     fileStamp
	stagedUntracked [sha256.Size]byte
	// diffedAt is the worktree's git state when its diff was last computed;
	// see ChangedSinceDiff.
	diffedAt worktreeStamp
}

// worktreePaths are the files the diff path reads or watches.
type worktreePaths struct {
	private string // Adroit's own index; see stageUntrackedPrivately
	index   string // the worktree's real index
	head    string // the worktree's HEAD
	common  string // the repository's common git directory, where branch refs live
}

// fileStamp is a file's size and modification time: enough to tell that it was
// rewritten, without reading it. The zero value is a file that does not exist.
type fileStamp struct {
	size  int64
	mtime int64
}

func stampOf(path string) fileStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{size: fi.Size(), mtime: fi.ModTime().UnixNano()}
}

// worktreeStamp is the worktree's git state as the filesystem shows it: its
// index, its HEAD, and the branch HEAD points at.
type worktreeStamp struct {
	index, head, ref fileStamp
	headRef          string
}

// stamp reads the worktree's git state without forking. A packed branch ref
// has no file of its own and stamps as absent, which is harmless: a commit
// writes it loose again, and rewrites the index besides.
func (p *worktreePaths) stamp() worktreeStamp {
	s := worktreeStamp{index: stampOf(p.index), head: stampOf(p.head)}
	if b, err := os.ReadFile(p.head); err == nil {
		if ref, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "ref: "); ok {
			s.headRef = ref
			s.ref = stampOf(filepath.Join(p.common, filepath.FromSlash(ref)))
		}
	}
	return s
}

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

// worktreePaths resolves, once, where this worktree's indexes and HEAD live.
// Cached, since none of it can change for the life of the worktree and every
// diff read on every tick needs it.
func (g *GitWorktree) worktreePaths() (*worktreePaths, error) {
	g.diff.mu.Lock()
	defer g.diff.mu.Unlock()
	if g.diff.paths != nil {
		return g.diff.paths, nil
	}
	out, err := g.runGitCommand(g.worktreePath, "rev-parse", "--git-path", privateIndexName,
		"--git-path", "index", "--git-path", "HEAD", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		return nil, fmt.Errorf("could not locate the worktree's git directory: %q", out)
	}
	for i, l := range lines {
		l = strings.TrimSpace(l)
		if !filepath.IsAbs(l) {
			l = filepath.Join(g.worktreePath, l)
		}
		lines[i] = l
	}
	g.diff.paths = &worktreePaths{private: lines[0], index: lines[1], head: lines[2], common: lines[3]}
	return g.diff.paths, nil
}

// privateIndexPath is where Adroit's own index for this worktree lives.
func (g *GitWorktree) privateIndexPath() (string, error) {
	p, err := g.worktreePaths()
	if err != nil {
		return "", err
	}
	return p.private, nil
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
//
// The copy is skipped when it would come out the same: the real index has not
// been rewritten and the set of untracked files is the one already staged.
// Copying regardless wrote the whole index -- 2.6MB on a large repository -- and
// then rewrote it with the add, for every session every fifteen seconds and for
// the selected one on every diff: megabytes a second of disk writes to restate
// nothing. Listing the untracked files is a read, and is what the add walks
// anyway.
func (g *GitWorktree) stageUntrackedPrivately() error {
	paths, err := g.worktreePaths()
	if err != nil {
		return err
	}
	index := stampOf(paths.index)
	// Against the real index, which is what decides what is untracked and what
	// the add below starts from.
	untracked, err := g.runGitCommand(g.worktreePath, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(untracked))

	g.diff.mu.Lock()
	unchanged := g.diff.staged && g.diff.stagedIndex == index && g.diff.stagedUntracked == sum
	g.diff.mu.Unlock()
	if unchanged {
		if _, err := os.Stat(paths.private); err == nil {
			return nil
		}
	}

	data, err := os.ReadFile(paths.index)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not read the index: %w", err)
	}
	g.diff.mu.Lock()
	g.diff.staged = false
	g.diff.mu.Unlock()
	tmp := paths.private + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("could not copy the index: %w", err)
	}
	if err := os.Rename(tmp, paths.private); err != nil {
		return fmt.Errorf("could not copy the index: %w", err)
	}
	if _, err := g.runGitCommandEnv(g.worktreePath, gitCommandTimeout, []string{"GIT_INDEX_FILE=" + paths.private},
		"add", "-N", "."); err != nil {
		return err
	}
	// Stamped with what the index was before the copy: had it been rewritten
	// in between, the next staging sees a different stamp and copies again.
	g.diff.mu.Lock()
	g.diff.staged, g.diff.stagedIndex, g.diff.stagedUntracked = true, index, sum
	g.diff.mu.Unlock()
	return nil
}

// ChangedSinceDiff reports whether the worktree's index, HEAD or branch has been
// rewritten since the last diff was computed -- a commit, an add, a checkout --
// without forking. False before the first diff, which is due anyway.
func (g *GitWorktree) ChangedSinceDiff() bool {
	g.diff.mu.Lock()
	paths, at := g.diff.paths, g.diff.diffedAt
	g.diff.mu.Unlock()
	if paths == nil {
		return false
	}
	return paths.stamp() != at
}

// markDiffed records the worktree's git state a diff is about to be computed
// from, for ChangedSinceDiff. Taken before the diff, so a change during it is
// caught next time rather than absorbed.
func (g *GitWorktree) markDiffed() {
	paths, err := g.worktreePaths()
	if err != nil {
		return
	}
	st := paths.stamp()
	g.diff.mu.Lock()
	g.diff.diffedAt = st
	g.diff.mu.Unlock()
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
	g.markDiffed()

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
	g.markDiffed()

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
//
// Re-resolving is a dozen git calls, so the answer is kept for as long as what
// it was worked out from has not moved: HEAD, the candidate refs and the
// recorded SHA, read in one call (baseKey). It used to be kept for 30 seconds
// instead, which re-ran the whole resolution twice a minute for every session in
// the list while nothing had changed at all.
func (g *GitWorktree) resolveDiffBase() (string, error) {
	key := g.baseKey()

	g.diff.mu.Lock()
	cached, cachedKey, at := g.diff.resolvedBase, g.diff.baseKey, g.diff.resolvedBaseAt
	g.diff.mu.Unlock()
	if cached != "" && key != "" && key == cachedKey && time.Since(at) < diffBaseMaxAge {
		return cached, nil
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

	g.diff.mu.Lock()
	g.diff.resolvedBase, g.diff.baseKey, g.diff.resolvedBaseAt = best, key, time.Now()
	g.diff.mu.Unlock()
	return best, nil
}

// baseCandidateNames are the short names resolveDiffBase may measure from.
func (g *GitWorktree) baseCandidateNames() []string {
	var names []string
	if g.baseBranch != "" {
		names = append(names, "origin/"+g.baseBranch, g.baseBranch)
	}
	return append(names, "origin/main", "origin/master", "main", "master")
}

// baseKey fingerprints everything the resolved base depends on, in one fork:
// HEAD, every ref a candidate could name, and origin/HEAD. Over-inclusive on
// purpose -- a ref that is not a candidate only costs a re-resolve when it
// moves. "" when git cannot say, which is never treated as a match.
func (g *GitWorktree) baseKey() string {
	args := []string{"show-ref", "--head", "refs/remotes/origin/HEAD"}
	for _, name := range g.baseCandidateNames() {
		args = append(args, "refs/heads/"+name, "refs/remotes/"+name)
	}
	out, err := g.runGitCommand(g.worktreePath, args...)
	if err != nil {
		return ""
	}
	return g.baseCommitSHA + "\n" + out
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
	for _, ref := range g.baseCandidateNames() {
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
