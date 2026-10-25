package gitx

import (
	"bytes"
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

func (r *Repo) Commits(ctx context.Context, base string, limit int) ([]Commit, error) {
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
	if base != "" {
		if err := r.verifyRef(ctx, base); err != nil {
			return nil, err
		}
		args = append(args, base+"..HEAD")
	} else {
		args = append(args, "HEAD")
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
