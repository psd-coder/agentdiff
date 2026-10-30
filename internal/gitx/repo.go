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
	if err != nil || root == "" {
		return nil, fmt.Errorf("%w: %s", ErrNotRepo, abs)
	}
	return &Repo{Root: root}, nil
}

func (r *Repo) Name() string { return filepath.Base(r.Root) }

func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	name, _, err := r.optionalLine(ctx, "symbolic-ref", "-q", "--short", "HEAD")
	return name, err
}

func (r *Repo) HasHead(ctx context.Context) bool {
	_, ok, err := r.optionalLine(ctx, "rev-parse", "-q", "--verify", "HEAD^{commit}")
	return ok && err == nil
}

func (r *Repo) DefaultBase(ctx context.Context) string {
	if ref, ok, _ := r.optionalLine(ctx, "symbolic-ref", "-q", "--short", "refs/remotes/origin/HEAD"); ok {
		return ref
	}
	for _, name := range []string{"main", "master"} {
		if _, ok, _ := r.optionalLine(ctx, "rev-parse", "-q", "--verify", "refs/heads/"+name); ok {
			return name
		}
	}
	return ""
}

func (r *Repo) Branches(ctx context.Context) ([]string, error) {
	out, err := r.output(ctx, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
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

// Commits lists the commits of head, newest first: those not on base
// when base is set. An empty head means HEAD.
func (r *Repo) Commits(ctx context.Context, base, head string, limit int) ([]Commit, error) {
	if !r.HasHead(ctx) {
		return []Commit{}, nil
	}
	if limit <= 0 {
		limit = defaultCommitLimit
	}
	args := []string{
		"log", "-z", "--no-show-signature", "--no-color",
		"--format=%H%x00%h%x00%an%x00%aI%x00%s%x00%b",
		"-n", strconv.Itoa(limit),
	}
	head = cmp.Or(head, "HEAD")
	if err := r.verifyRef(ctx, head); err != nil {
		return nil, err
	}
	if base != "" {
		if err := r.verifyRef(ctx, base); err != nil {
			return nil, err
		}
		args = append(args, base+".."+head)
	} else {
		args = append(args, head)
	}
	args = append(args, "--")
	out, err := r.output(ctx, args...)
	if err != nil {
		return nil, err
	}
	return parseCommits(out)
}

func parseCommits(out []byte) ([]Commit, error) {
	fields := splitNUL(out)
	if len(fields)%commitFields != 0 {
		return nil, fmt.Errorf("gitx: unexpected git log output (%d fields)", len(fields))
	}
	commits := make([]Commit, 0, len(fields)/commitFields)
	for i := 0; i < len(fields); i += commitFields {
		f := fields[i : i+commitFields]
		t, err := time.Parse(time.RFC3339, f[3])
		if err != nil {
			return nil, fmt.Errorf("gitx: commit %s: %w", f[0], err)
		}
		commits = append(commits, Commit{
			SHA:     strings.TrimSpace(f[0]),
			Short:   f[1],
			Author:  f[2],
			Time:    t,
			Subject: f[4],
			Body:    strings.TrimSpace(f[5]),
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

func (r *Repo) verifyRef(ctx context.Context, ref string) error {
	_, err := r.resolveCommit(ctx, ref)
	return err
}

func (r *Repo) resolveCommit(ctx context.Context, ref string) (string, error) {
	if ref == "" || strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("gitx: invalid ref %q", ref)
	}
	sha, ok, err := r.optionalLine(ctx, "rev-parse", "-q", "--verify", ref+"^{commit}")
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
func (r *Repo) GitDir(ctx context.Context) (string, error) {
	return r.line(ctx, "rev-parse", "--absolute-git-dir")
}

// CommonDir is the git directory that every working tree of the
// repository shares, with the refs. It is GitDir in the main working tree.
func (r *Repo) CommonDir(ctx context.Context) (string, error) {
	return r.line(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
}

// Ignored reports which of paths, relative to the root, the gitignore
// rules leave out.
func (r *Repo) Ignored(ctx context.Context, paths []string) (map[string]bool, error) {
	var in, out bytes.Buffer
	for _, p := range paths {
		in.WriteString(p)
		in.WriteByte(0)
	}
	err := runIn(ctx, r.Root, &in, &out, "check-ignore", "--stdin", "-z")
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
func (r *Repo) Worktrees(ctx context.Context) ([]Worktree, error) {
	out, err := r.output(ctx, "worktree", "list", "--porcelain")
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
