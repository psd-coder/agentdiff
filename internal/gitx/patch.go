package gitx

import (
	"context"
	"fmt"
	"strings"
)

const statWidth = 40

func (r *Repo) DiffStat(ctx context.Context, s Scope) (string, error) {
	v, err := r.resolve(ctx, s)
	if err != nil {
		return "", err
	}
	out, err := r.diff(ctx, v, "--stat=200")
	if err != nil {
		return "", err
	}
	if !v.worktree() {
		return string(out), nil
	}
	untracked, err := r.untrackedChanges(ctx)
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

func untrackedStatLine(c FileChange) string {
	if c.Binary {
		return fmt.Sprintf(" %s | Bin (untracked)\n", c.Path)
	}
	return fmt.Sprintf(" %s | %d %s (untracked)\n", c.Path, c.Additions, strings.Repeat("+", min(c.Additions, statWidth)))
}

// Patch returns the unified diff of the scope. maxBytes <= 0 means no limit. A truncated
// patch is cut at a line boundary.
func (r *Repo) Patch(ctx context.Context, s Scope, maxBytes int) (string, bool, error) {
	v, err := r.resolve(ctx, s)
	if err != nil {
		return "", false, err
	}
	out := &cappedBuffer{max: maxBytes}
	args := append([]string{"diff"}, diffFlags...)
	args = append(args, "--src-prefix=a/", "--dst-prefix=b/")
	args = append(append(args, v.args()...), "--")
	if err := r.run(ctx, out, args...); err != nil {
		return "", false, err
	}
	if v.worktree() {
		if err := r.untrackedPatches(ctx, out); err != nil {
			return "", false, err
		}
	}
	return out.String(), out.truncated, nil
}

func (r *Repo) untrackedPatches(ctx context.Context, out *cappedBuffer) error {
	paths, err := r.untrackedPaths(ctx)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if out.full() {
			out.truncated = true
			return nil
		}
		err := r.run(ctx, out, "diff", "--no-index", "--no-color", "--no-ext-diff", "--no-textconv",
			"--src-prefix=a/", "--dst-prefix=b/", "--", "/dev/null", p)
		// --no-index exits 1 when the files differ, which is always the case here.
		if err != nil && exitCode(err) != 1 {
			return err
		}
	}
	return nil
}
