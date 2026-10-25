package gitx

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type FileStatus string

const (
	StatusAdded      FileStatus = "added"
	StatusModified   FileStatus = "modified"
	StatusDeleted    FileStatus = "deleted"
	StatusRenamed    FileStatus = "renamed"
	StatusCopied     FileStatus = "copied"
	StatusTypeChange FileStatus = "typechange"
)

type FileChange struct {
	Path      string     `json:"path"`
	OldPath   string     `json:"oldPath,omitempty"`
	Status    FileStatus `json:"status"`
	Untracked bool       `json:"untracked,omitempty"`
	Binary    bool       `json:"binary,omitempty"`
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
}

// revs is the pair of trees a scope compares. An empty to means the working tree.
type revs struct {
	from, to string
}

func (v revs) args() []string {
	if v.to == "" {
		return []string{v.from}
	}
	return []string{v.from, v.to}
}

func (v revs) worktree() bool { return v.to == "" }

func (r *Repo) resolve(ctx context.Context, s Scope) (revs, error) {
	switch s.Kind {
	case ScopeUncommitted:
		if !r.HasHead(ctx) {
			return revs{from: emptyTree}, nil
		}
		return revs{from: "HEAD"}, nil
	case ScopeBranch:
		return r.resolveBranch(ctx, s.Base)
	case ScopeCommit:
		return r.resolveCommitScope(ctx, s.Commit)
	case ScopeRange:
		from, err := r.resolveCommit(ctx, s.From)
		if err != nil {
			return revs{}, err
		}
		to, err := r.resolveCommit(ctx, s.To)
		if err != nil {
			return revs{}, err
		}
		return revs{from: from, to: to}, nil
	}
	return revs{}, fmt.Errorf("gitx: unknown scope kind %q", s.Kind)
}

func (r *Repo) resolveBranch(ctx context.Context, base string) (revs, error) {
	if _, err := r.resolveCommit(ctx, base); err != nil {
		return revs{}, err
	}
	head, err := r.resolveCommit(ctx, "HEAD")
	if err != nil {
		return revs{}, err
	}
	mb, ok, err := r.optionalLine(ctx, "merge-base", base, head)
	if err != nil {
		return revs{}, err
	}
	if !ok {
		return revs{}, fmt.Errorf("gitx: %s and HEAD have no common ancestor", base)
	}
	return revs{from: mb, to: head}, nil
}

func (r *Repo) resolveCommitScope(ctx context.Context, commit string) (revs, error) {
	sha, err := r.resolveCommit(ctx, commit)
	if err != nil {
		return revs{}, err
	}
	parent, ok, err := r.optionalLine(ctx, "rev-parse", "-q", "--verify", sha+"^1")
	if err != nil {
		return revs{}, err
	}
	if !ok {
		parent = emptyTree
	}
	return revs{from: parent, to: sha}, nil
}

var diffFlags = []string{"--no-color", "--no-ext-diff", "--no-textconv", "-M"}

func (r *Repo) diff(ctx context.Context, v revs, format ...string) ([]byte, error) {
	args := append([]string{"diff"}, diffFlags...)
	args = append(args, format...)
	args = append(args, v.args()...)
	return r.output(ctx, append(args, "--")...)
}

func (r *Repo) Changes(ctx context.Context, s Scope) ([]FileChange, error) {
	v, err := r.resolve(ctx, s)
	if err != nil {
		return nil, err
	}
	changes, err := r.trackedChanges(ctx, v)
	if err != nil {
		return nil, err
	}
	if v.worktree() {
		untracked, err := r.untrackedChanges(ctx)
		if err != nil {
			return nil, err
		}
		changes = append(changes, untracked...)
	}
	slices.SortFunc(changes, func(a, b FileChange) int { return strings.Compare(a.Path, b.Path) })
	return changes, nil
}

func (r *Repo) trackedChanges(ctx context.Context, v revs) ([]FileChange, error) {
	names, err := r.diff(ctx, v, "--name-status", "-z")
	if err != nil {
		return nil, err
	}
	changes, err := parseNameStatus(names)
	if err != nil {
		return nil, err
	}
	nums, err := r.diff(ctx, v, "--numstat", "-z")
	if err != nil {
		return nil, err
	}
	stats, err := parseNumstat(nums)
	if err != nil {
		return nil, err
	}
	for i := range changes {
		st := stats[changes[i].Path]
		changes[i].Additions, changes[i].Deletions, changes[i].Binary = st.add, st.del, st.binary
	}
	return changes, nil
}

func parseNameStatus(out []byte) ([]FileChange, error) {
	fields := splitNUL(out)
	var changes []FileChange
	for i := 0; i < len(fields); {
		code := fields[i]
		if code == "" {
			return nil, fmt.Errorf("gitx: empty status in name-status output")
		}
		status := statusOf(code[0])
		paths := 1
		if status == StatusRenamed || status == StatusCopied {
			paths = 2
		}
		if i+paths >= len(fields) {
			return nil, fmt.Errorf("gitx: truncated name-status output")
		}
		c := FileChange{Status: status, Path: fields[i+paths]}
		if paths == 2 {
			c.OldPath = fields[i+1]
		}
		changes = append(changes, c)
		i += paths + 1
	}
	return changes, nil
}

func statusOf(code byte) FileStatus {
	switch code {
	case 'A':
		return StatusAdded
	case 'D':
		return StatusDeleted
	case 'R':
		return StatusRenamed
	case 'C':
		return StatusCopied
	case 'T':
		return StatusTypeChange
	}
	return StatusModified
}

type numstat struct {
	add, del int
	binary   bool
}

// parseNumstat keys stats by new path. A rename record has an empty path field
// followed by the old and new paths as two more NUL-terminated fields.
func parseNumstat(out []byte) (map[string]numstat, error) {
	fields := splitNUL(out)
	stats := make(map[string]numstat, len(fields))
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("gitx: unexpected numstat record %q", fields[i])
		}
		path := parts[2]
		if path == "" {
			if i+2 >= len(fields) {
				return nil, fmt.Errorf("gitx: truncated numstat output")
			}
			path = fields[i+2]
			i += 2
		}
		st, err := parseCounts(parts[0], parts[1])
		if err != nil {
			return nil, err
		}
		stats[path] = st
	}
	return stats, nil
}

func parseCounts(add, del string) (numstat, error) {
	if add == "-" && del == "-" {
		return numstat{binary: true}, nil
	}
	a, err := strconv.Atoi(add)
	if err != nil {
		return numstat{}, fmt.Errorf("gitx: numstat additions %q: %w", add, err)
	}
	d, err := strconv.Atoi(del)
	if err != nil {
		return numstat{}, fmt.Errorf("gitx: numstat deletions %q: %w", del, err)
	}
	return numstat{add: a, del: d}, nil
}

func (r *Repo) untrackedPaths(ctx context.Context) ([]string, error) {
	out, err := r.output(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range splitNUL(out) {
		// Nested repositories are listed as directories with a trailing slash.
		if p != "" && !strings.HasSuffix(p, "/") {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func (r *Repo) untrackedChanges(ctx context.Context) ([]FileChange, error) {
	paths, err := r.untrackedPaths(ctx)
	if err != nil {
		return nil, err
	}
	changes := make([]FileChange, 0, len(paths))
	for _, p := range paths {
		lines, binary, err := r.countWorktreeLines(p)
		if err != nil {
			return nil, err
		}
		changes = append(changes, FileChange{
			Path:      p,
			Status:    StatusAdded,
			Untracked: true,
			Binary:    binary,
			Additions: lines,
		})
	}
	return changes, nil
}

const binarySniffLen = 8000

func (r *Repo) countWorktreeLines(path string) (int, bool, error) {
	full := filepath.Join(r.Root, path)
	info, err := os.Lstat(full)
	if err != nil {
		return 0, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 1, false, nil
	}
	f, err := os.Open(full)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, binarySniffLen)
	head, err := br.Peek(binarySniffLen)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return 0, false, err
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return 0, true, nil
	}
	lines, err := countLines(br)
	return lines, false, err
}

// countLines matches git: a final line without a newline still counts.
func countLines(r io.Reader) (int, error) {
	buf := make([]byte, 32*1024)
	lines := 0
	endsWithNewline := true
	for {
		n, err := r.Read(buf)
		if n > 0 {
			lines += bytes.Count(buf[:n], []byte{'\n'})
			endsWithNewline = buf[n-1] == '\n'
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	if !endsWithNewline {
		lines++
	}
	return lines, nil
}
