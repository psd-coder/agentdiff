package gitx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const maxContentSize = 2 << 20

func (r *Repo) Contents(ctx context.Context, s Scope, f FileChange) (old, new string, err error) {
	if f.Binary {
		return "", "", ErrBinary
	}
	if !validPath(f.Path) || (f.OldPath != "" && !validPath(f.OldPath)) {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidPath, f.Path)
	}
	v, err := r.resolve(ctx, s)
	if err != nil {
		return "", "", err
	}
	if f.Status != StatusAdded {
		oldPath := f.Path
		if f.OldPath != "" {
			oldPath = f.OldPath
		}
		if old, err = r.blob(ctx, v.from, oldPath); err != nil {
			return "", "", err
		}
	}
	if f.Status != StatusDeleted {
		if v.worktree() {
			new, err = r.worktreeFile(ctx, f.Path)
		} else {
			new, err = r.blob(ctx, v.to, f.Path)
		}
		if err != nil {
			return "", "", err
		}
	}
	return old, new, nil
}

func validPath(p string) bool {
	return p != "" && filepath.IsLocal(filepath.FromSlash(p))
}

func (r *Repo) blob(ctx context.Context, rev, path string) (string, error) {
	spec := rev + ":" + path
	oid, kind, size, err := r.objectInfo(ctx, spec)
	if err != nil {
		return "", err
	}
	// A submodule is stored as a commit entry; git diff shows it as this one line.
	if kind == "commit" {
		return "Subproject commit " + oid + "\n", nil
	}
	if size > maxContentSize {
		return "", ErrTooLarge
	}
	out, err := r.output(ctx, "cat-file", "blob", oid)
	if err != nil {
		return "", err
	}
	return text(out)
}

func (r *Repo) objectInfo(ctx context.Context, spec string) (oid, kind string, size int64, err error) {
	oid, ok, err := r.optionalLine(ctx, "rev-parse", "-q", "--verify", spec)
	if err != nil {
		return "", "", 0, err
	}
	if !ok {
		return "", "", 0, fmt.Errorf("gitx: %s does not exist", spec)
	}
	var out bytes.Buffer
	stdin := strings.NewReader(oid + "\n")
	err = runIn(ctx, r.Root, stdin, &out, "cat-file", "--batch-check=%(objecttype) %(objectsize)")
	if err != nil {
		return "", "", 0, err
	}
	kind, sizeText, found := strings.Cut(strings.TrimSpace(out.String()), " ")
	if !found {
		return "", "", 0, fmt.Errorf("gitx: %s: %s", spec, strings.TrimSpace(out.String()))
	}
	size, err = strconv.ParseInt(sizeText, 10, 64)
	return oid, kind, size, err
}

func (r *Repo) worktreeFile(ctx context.Context, path string) (string, error) {
	full := filepath.Join(r.Root, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if err != nil {
		return "", err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return os.Readlink(full)
	case info.IsDir():
		var out bytes.Buffer
		if err := runIn(ctx, full, nil, &out, "rev-parse", "HEAD"); err != nil {
			return "", err
		}
		return "Subproject commit " + strings.TrimSpace(out.String()) + "\n", nil
	case info.Size() > maxContentSize:
		return "", ErrTooLarge
	}
	file, err := os.Open(full)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxContentSize+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxContentSize {
		return "", ErrTooLarge
	}
	return text(data)
}

func text(data []byte) (string, error) {
	if bytes.IndexByte(data[:min(len(data), binarySniffLen)], 0) >= 0 {
		return "", ErrBinary
	}
	return string(data), nil
}
