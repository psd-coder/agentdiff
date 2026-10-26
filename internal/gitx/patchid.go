package gitx

import (
	"bytes"
	"cmp"
	"context"
	"strconv"
	"strings"
)

// PatchID identifies the content of a scope's changes, the same way for
// uncommitted changes and for the commit that later holds them. It is
// empty when there are no changes. Line numbers and whitespace do not
// count, as in `git patch-id --stable`.
func (r *Repo) PatchID(ctx context.Context, s Scope) (string, error) {
	v, err := r.resolve(ctx, s)
	if err != nil {
		return "", err
	}
	patch, err := r.diff(ctx, v, "-p")
	if err != nil {
		return "", err
	}
	if v.worktree() {
		paths, err := r.untrackedPaths(ctx)
		if err != nil {
			return "", err
		}
		for _, p := range paths {
			var out bytes.Buffer
			args := append([]string{"diff"}, diffFlags...)
			// --no-index exits with 1 when the files differ, which they always do.
			if err := r.run(ctx, &out, append(args, "--no-index", "--", "/dev/null", p)...); err != nil && exitCode(err) != 1 {
				return "", err
			}
			patch = append(patch, out.Bytes()...)
		}
	}
	ids, err := r.patchIDs(ctx, patch)
	if err != nil || len(ids) == 0 {
		return "", err
	}
	return ids[0][0], nil
}

// CommitPatchIDs maps the patch IDs of the commits Commits lists to their
// hashes. A merge commit counts as its changes against the first parent.
func (r *Repo) CommitPatchIDs(ctx context.Context, base, head string, limit int) (map[string]string, error) {
	if !r.HasHead(ctx) {
		return map[string]string{}, nil
	}
	head = cmp.Or(head, "HEAD")
	rng := head
	if base != "" {
		rng = base + ".." + head
	}
	args := append([]string{"log", "-p", "--no-show-signature", "--diff-merges=first-parent", "--format=commit %H"}, diffFlags...)
	args = append(args, "-n", strconv.Itoa(cmp.Or(limit, defaultCommitLimit)), rng, "--")
	log, err := r.output(ctx, args...)
	if err != nil {
		return nil, err
	}
	ids, err := r.patchIDs(ctx, log)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, id := range ids {
		if _, ok := out[id[0]]; !ok {
			out[id[0]] = id[1]
		}
	}
	return out, nil
}

// patchIDs runs a patch through git patch-id: one pair of patch ID and
// commit hash per patch.
func (r *Repo) patchIDs(ctx context.Context, patch []byte) ([][2]string, error) {
	var out bytes.Buffer
	if err := runIn(ctx, r.Root, bytes.NewReader(patch), &out, "patch-id", "--stable"); err != nil {
		return nil, err
	}
	var ids [][2]string
	for line := range strings.Lines(out.String()) {
		if id, sha, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			ids = append(ids, [2]string{id, sha})
		}
	}
	return ids, nil
}
