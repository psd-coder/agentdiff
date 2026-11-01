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

func (repo *Repo) Snapshot(ctx context.Context, scope Scope) (*Snapshot, error) {
	revisions, err := repo.resolve(ctx, scope)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{repo: repo, revs: revisions}
	if revisions.worktree() {
		if snap.untracked, err = repo.untrackedPaths(ctx); err != nil {
			return nil, err
		}
	}
	return snap, nil
}

func (snap *Snapshot) Changes(ctx context.Context) ([]FileChange, error) {
	changes, err := snap.repo.trackedChanges(ctx, snap.revs)
	if err != nil {
		return nil, err
	}
	untracked, err := snap.repo.untrackedChanges(snap.untracked)
	if err != nil {
		return nil, err
	}
	changes = append(changes, untracked...)
	slices.SortFunc(changes, func(a, b FileChange) int { return strings.Compare(a.Path, b.Path) })
	return changes, nil
}

func (snap *Snapshot) DiffStat(ctx context.Context) (string, error) {
	out, err := snap.repo.diff(ctx, snap.revs, "--stat=200")
	if err != nil {
		return "", err
	}
	untracked, err := snap.repo.untrackedChanges(snap.untracked)
	if err != nil {
		return "", err
	}
	var stat strings.Builder
	stat.Write(out)
	for _, c := range untracked {
		stat.WriteString(untrackedStatLine(c))
	}
	return stat.String(), nil
}

const statWidth = 40

func untrackedStatLine(fileChange FileChange) string {
	if fileChange.Binary {
		return fmt.Sprintf(" %s | Bin (untracked)\n", fileChange.Path)
	}
	return fmt.Sprintf(" %s | %d %s (untracked)\n", fileChange.Path, fileChange.Additions, strings.Repeat("+", min(fileChange.Additions, statWidth)))
}

// Contents reads the old and the new side of f.
func (snap *Snapshot) Contents(ctx context.Context, fileChange FileChange) (old, new string, err error) {
	if err := checkFile(fileChange); err != nil {
		return "", "", err
	}
	return snap.repo.sides(ctx, snap.revs, fileChange, snap.blob(ctx))
}

// File reads path as it is at the end of the snapshot, for a file that
// is not in its changes.
func (snap *Snapshot) File(ctx context.Context, path string) (string, error) {
	if err := checkPath(path); err != nil {
		return "", err
	}
	return snap.repo.newSide(ctx, snap.revs, path, snap.blob(ctx))
}

func (snap *Snapshot) blob(ctx context.Context) func(rev, path string) (string, error) {
	return func(rev, path string) (string, error) { return snap.repo.blob(ctx, rev, path) }
}
