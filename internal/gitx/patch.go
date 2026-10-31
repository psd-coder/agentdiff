package gitx

import (
	"context"
	"fmt"
	"strings"
)

const statWidth = 40

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
	args = append(append(args, v.args()...), "--")
	if err := r.run(ctx, out, args...); err != nil {
		return "", false, err
	}
	if v.worktree() {
		paths, err := r.untrackedPaths(ctx)
		if err != nil {
			return "", false, err
		}
		if err := r.untrackedPatches(ctx, paths, out); err != nil {
			return "", false, err
		}
	}
	return out.String(), out.truncated, nil
}

// untrackedPatches writes the untracked files in paths as added ones. git
// writes nothing and exits with 1 for a file deleted since paths was
// listed, so that file is left out.
func (r *Repo) untrackedPatches(ctx context.Context, paths []string, out *cappedBuffer) error {
	for _, p := range paths {
		if out.full() {
			out.truncated = true
			return nil
		}
		args := append([]string{"diff"}, diffFlags...)
		err := r.run(ctx, out, append(args, "--no-index", "--", "/dev/null", p)...)
		// --no-index exits 1 when the files differ, which is always the case here.
		if err != nil && exitCode(err) != 1 {
			return err
		}
	}
	return nil
}
