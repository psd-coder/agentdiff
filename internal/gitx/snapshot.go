package gitx

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Snapshot is a scope resolved once, so that its changes, diffstat,
// fingerprint and file contents all describe the same commits, even when
// HEAD moves while they are read. For uncommitted changes HEAD is pinned
// to the commit it named when the snapshot was made, and the working tree
// side is read live on every call. The list of untracked files is also
// read once, when the snapshot is made.
type Snapshot struct {
	repo      *Repo
	revs      revs
	untracked []string
}

func (r *Repo) Snapshot(ctx context.Context, s Scope) (*Snapshot, error) {
	v, err := r.resolve(ctx, s)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{repo: r, revs: v}
	if v.worktree() {
		if snap.untracked, err = r.untrackedPaths(ctx); err != nil {
			return nil, err
		}
	}
	return snap, nil
}

func (s *Snapshot) Changes(ctx context.Context) ([]FileChange, error) {
	changes, err := s.repo.trackedChanges(ctx, s.revs)
	if err != nil {
		return nil, err
	}
	untracked, err := s.repo.untrackedChanges(s.untracked)
	if err != nil {
		return nil, err
	}
	changes = append(changes, untracked...)
	slices.SortFunc(changes, func(a, b FileChange) int { return strings.Compare(a.Path, b.Path) })
	return changes, nil
}

func (s *Snapshot) DiffStat(ctx context.Context) (string, error) {
	out, err := s.repo.diff(ctx, s.revs, "--stat=200")
	if err != nil {
		return "", err
	}
	untracked, err := s.repo.untrackedChanges(s.untracked)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.Write(out)
	for _, c := range untracked {
		b.WriteString(untrackedStatLine(c))
	}
	return b.String(), nil
}

const statWidth = 40

func untrackedStatLine(c FileChange) string {
	if c.Binary {
		return fmt.Sprintf(" %s | Bin (untracked)\n", c.Path)
	}
	return fmt.Sprintf(" %s | %d %s (untracked)\n", c.Path, c.Additions, strings.Repeat("+", min(c.Additions, statWidth)))
}

// Contents reads the old and the new side of f.
func (s *Snapshot) Contents(ctx context.Context, f FileChange) (old, new string, err error) {
	if err := checkFile(f); err != nil {
		return "", "", err
	}
	return s.repo.sides(ctx, s.revs, f, s.blob(ctx))
}

// File reads path as it is at the end of the snapshot, for a file that
// is not in its changes.
func (s *Snapshot) File(ctx context.Context, path string) (string, error) {
	if err := checkPath(path); err != nil {
		return "", err
	}
	return s.repo.newSide(ctx, s.revs, path, s.blob(ctx))
}

func (s *Snapshot) blob(ctx context.Context) func(rev, path string) (string, error) {
	return func(rev, path string) (string, error) { return s.repo.blob(ctx, rev, path) }
}
