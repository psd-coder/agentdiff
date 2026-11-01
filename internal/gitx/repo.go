package gitx

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Repo struct {
	Root string
}

func Open(path string) (*Repo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	var out bytes.Buffer
	err = runIn(context.Background(), abs, nil, &out, "rev-parse", "--show-toplevel")
	root := strings.TrimSpace(out.String())
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrNotRepo, abs, err)
	}
	if root == "" {
		return nil, fmt.Errorf("%w: %s", ErrNotRepo, abs)
	}
	return &Repo{Root: root}, nil
}

func (repo *Repo) Name() string { return filepath.Base(repo.Root) }

func (repo *Repo) CurrentBranch(ctx context.Context) (string, error) {
	name, _, err := repo.optionalLine(ctx, "symbolic-ref", "-q", "--short", "HEAD")
	return name, err
}

func (repo *Repo) HasHead(ctx context.Context) bool {
	_, ok, err := repo.optionalLine(ctx, "rev-parse", "-q", "--verify", "HEAD^{commit}")
	return ok && err == nil
}

func (repo *Repo) DefaultBase(ctx context.Context) string {
	if ref, ok, _ := repo.optionalLine(ctx, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); ok {
		return ref
	}
	for _, name := range []string{"main", "master"} {
		if _, ok, _ := repo.optionalLine(ctx, "rev-parse", "-q", "--verify", "refs/heads/"+name); ok {
			return name
		}
	}
	return ""
}

func (repo *Repo) Branches(ctx context.Context) ([]string, error) {
	out, err := repo.output(ctx, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	var local, remote []string
	for ref := range strings.Lines(string(out)) {
		ref = strings.TrimSuffix(ref, "\n")
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			local = append(local, strings.TrimPrefix(ref, "refs/heads/"))
		case strings.HasPrefix(ref, "refs/remotes/") && !strings.HasSuffix(ref, "/HEAD"):
			remote = append(remote, strings.TrimPrefix(ref, "refs/remotes/"))
		}
	}
	return append(local, remote...), nil
}

type Commit struct {
	SHA     string    `json:"sha"`
	Short   string    `json:"short"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
	Author  string    `json:"author"`
	Time    time.Time `json:"time"`
}

const (
	defaultCommitLimit = 200
	commitFields       = 6
)

// CommitLog is the commits of a branch or a range, newest first, fixed to
// the commits its refs pointed to when it was made: its pages neither
// repeat nor skip a commit when the branch moves between them.
type CommitLog struct {
	repo *Repo
	// base and head are commit hashes. An empty head is a repository
	// without commits.
	base, head string
}

// CommitLog fixes the commits of head, those not on base when base is
// set. An empty head means HEAD.
func (repo *Repo) CommitLog(ctx context.Context, base, head string) (CommitLog, error) {
	if !repo.HasHead(ctx) {
		return CommitLog{repo: repo}, nil
	}
	head, err := repo.resolveCommit(ctx, cmp.Or(head, "HEAD"))
	if err != nil {
		return CommitLog{}, err
	}
	if base != "" {
		if base, err = repo.resolveCommit(ctx, base); err != nil {
			return CommitLog{}, err
		}
	}
	return CommitLog{repo: repo, base: base, head: head}, nil
}

func (commitLog CommitLog) revs() string {
	if commitLog.base == "" {
		return commitLog.head
	}
	return commitLog.base + ".." + commitLog.head
}

// Count is the number of commits in the log.
func (commitLog CommitLog) Count(ctx context.Context) (int, error) {
	if commitLog.head == "" {
		return 0, nil
	}
	out, err := commitLog.repo.line(ctx, "rev-list", "--count", commitLog.revs(), "--")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// Page lists up to limit commits of the log after the first skip.
func (commitLog CommitLog) Page(ctx context.Context, skip, limit int) ([]Commit, error) {
	if commitLog.head == "" {
		return []Commit{}, nil
	}
	out, err := commitLog.repo.output(ctx,
		"log", "-z", "--no-show-signature", "--no-color",
		"--format=%H%x00%h%x00%an%x00%aI%x00%s%x00%b",
		"--skip="+strconv.Itoa(skip), "-n", strconv.Itoa(limit), commitLog.revs(), "--")
	if err != nil {
		return nil, err
	}
	return parseCommits(out)
}

// Commit reads the commit that ref names.
func (repo *Repo) Commit(ctx context.Context, ref string) (Commit, error) {
	sha, err := repo.resolveCommit(ctx, ref)
	if err != nil {
		return Commit{}, err
	}
	commits, err := CommitLog{repo: repo, head: sha}.Page(ctx, 0, 1)
	if err != nil {
		return Commit{}, err
	}
	if len(commits) != 1 {
		return Commit{}, fmt.Errorf("gitx: commit %s not found", ref)
	}
	return commits[0], nil
}

func parseCommits(out []byte) ([]Commit, error) {
	fields := splitNUL(out)
	if len(fields)%commitFields != 0 {
		return nil, fmt.Errorf("gitx: unexpected git log output (%d fields)", len(fields))
	}
	commits := make([]Commit, 0, len(fields)/commitFields)
	for i := 0; i < len(fields); i += commitFields {
		record := fields[i : i+commitFields]
		date, err := time.Parse(time.RFC3339, record[3])
		if err != nil {
			return nil, fmt.Errorf("gitx: commit %s: %w", record[0], err)
		}
		commits = append(commits, Commit{
			SHA:     strings.TrimSpace(record[0]),
			Short:   record[1],
			Author:  record[2],
			Time:    date,
			Subject: record[4],
			Body:    strings.TrimSpace(record[5]),
		})
	}
	return commits, nil
}

func splitNUL(out []byte) []string {
	if len(out) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

func (repo *Repo) resolveCommit(ctx context.Context, ref string) (string, error) {
	if ref == "" || strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("gitx: invalid ref %q", ref)
	}
	sha, ok, err := repo.optionalLine(ctx, "rev-parse", "-q", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("gitx: unknown revision %q", ref)
	}
	return sha, nil
}

// GitDir is the repository's git directory, which git leaves out of every
// diff.
func (repo *Repo) GitDir(ctx context.Context) (string, error) {
	return repo.line(ctx, "rev-parse", "--absolute-git-dir")
}

// CommonDir is the git directory that every working tree of the
// repository shares, with the refs. It is GitDir in the main working tree.
func (repo *Repo) CommonDir(ctx context.Context) (string, error) {
	return repo.line(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
}

// Ignored reports which of paths, relative to the root, the gitignore
// rules leave out.
func (repo *Repo) Ignored(ctx context.Context, paths []string) (map[string]bool, error) {
	var in, out bytes.Buffer
	for _, p := range paths {
		in.WriteString(p)
		in.WriteByte(0)
	}
	err := runIn(ctx, repo.Root, &in, &out, "check-ignore", "--stdin", "-z")
	if err != nil && exitCode(err) != 1 {
		return nil, err
	}
	ignored := map[string]bool{}
	for _, p := range splitNUL(out.Bytes()) {
		ignored[p] = true
	}
	return ignored, nil
}

type Worktree struct {
	Path   string
	Branch string
}

// Worktrees lists the working trees of the repository, the main one
// first. Branch is empty for a detached HEAD.
func (repo *Repo) Worktrees(ctx context.Context) ([]Worktree, error) {
	out, err := repo.output(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var trees []Worktree
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSuffix(line, "\n")
		switch {
		case strings.HasPrefix(line, "worktree "):
			trees = append(trees, Worktree{Path: strings.TrimPrefix(line, "worktree ")})
		case strings.HasPrefix(line, "branch ") && len(trees) > 0:
			trees[len(trees)-1].Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	return trees, nil
}
