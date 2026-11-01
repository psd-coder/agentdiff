package gitx

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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

func (revs revs) args() []string {
	if revs.to == "" {
		return []string{revs.from}
	}
	return []string{revs.from, revs.to}
}

func (revs revs) worktree() bool { return revs.to == "" }

func (repo *Repo) resolve(ctx context.Context, scope Scope) (revs, error) {
	switch scope.Kind {
	case ScopeUncommitted:
		head, ok, err := repo.optionalLine(ctx, "rev-parse", "-q", "--verify", "HEAD^{commit}")
		if err != nil {
			return revs{}, err
		}
		if !ok {
			return revs{from: emptyTree}, nil
		}
		return revs{from: head}, nil
	case ScopeBranch:
		return repo.resolveBranch(ctx, scope.Base, cmp.Or(scope.Head, "HEAD"))
	case ScopeCommit:
		return repo.resolveCommitScope(ctx, scope.Commit)
	case ScopeRange:
		from, err := repo.resolveCommit(ctx, scope.From)
		if err != nil {
			return revs{}, err
		}
		to, err := repo.resolveCommit(ctx, scope.To)
		if err != nil {
			return revs{}, err
		}
		return revs{from: from, to: to}, nil
	default:
		return revs{}, fmt.Errorf("gitx: unknown scope kind %q", scope.Kind)
	}
}

func (repo *Repo) resolveBranch(ctx context.Context, base, branch string) (revs, error) {
	if _, err := repo.resolveCommit(ctx, base); err != nil {
		return revs{}, err
	}
	head, err := repo.resolveCommit(ctx, branch)
	if err != nil {
		return revs{}, err
	}
	mb, ok, err := repo.optionalLine(ctx, "merge-base", base, head)
	if err != nil {
		return revs{}, err
	}
	if !ok {
		return revs{}, fmt.Errorf("gitx: %s and %s have no common ancestor", base, branch)
	}
	return revs{from: mb, to: head}, nil
}

func (repo *Repo) resolveCommitScope(ctx context.Context, commit string) (revs, error) {
	sha, err := repo.resolveCommit(ctx, commit)
	if err != nil {
		return revs{}, err
	}
	parent, ok, err := repo.optionalLine(ctx, "rev-parse", "-q", "--verify", sha+"^1")
	if err != nil {
		return revs{}, err
	}
	if !ok {
		parent = emptyTree
	}
	return revs{from: parent, to: sha}, nil
}

// diffFlags pin the a/ and b/ prefixes, which diff.noprefix and
// diff.mnemonicPrefix change, so that paths and patch IDs stay the same.
var diffFlags = []string{
	"--no-color", "--no-ext-diff", "--no-textconv", "-M", "--src-prefix=a/", "--dst-prefix=b/",
}

func (repo *Repo) diff(ctx context.Context, revs revs, format ...string) ([]byte, error) {
	args := append([]string{"diff"}, diffFlags...)
	args = append(args, format...)
	args = append(args, revs.args()...)
	return repo.output(ctx, append(args, "--")...)
}

func (repo *Repo) trackedChanges(ctx context.Context, revs revs) ([]FileChange, error) {
	names, err := repo.diff(ctx, revs, "--name-status", "-z")
	if err != nil {
		return nil, err
	}
	changes, err := parseNameStatus(names)
	if err != nil {
		return nil, err
	}
	nums, err := repo.diff(ctx, revs, "--numstat", "-z")
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
		change := FileChange{Status: status, Path: fields[i+paths]}
		if paths == 2 {
			change.OldPath = fields[i+1]
		}
		changes = append(changes, change)
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
	additions, err := strconv.Atoi(add)
	if err != nil {
		return numstat{}, fmt.Errorf("gitx: numstat additions %q: %w", add, err)
	}
	deletions, err := strconv.Atoi(del)
	if err != nil {
		return numstat{}, fmt.Errorf("gitx: numstat deletions %q: %w", del, err)
	}
	return numstat{add: additions, del: deletions}, nil
}

func (repo *Repo) untrackedPaths(ctx context.Context) ([]string, error) {
	out, err := repo.output(ctx, "ls-files", "--others", "--exclude-standard", "-z")
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

// untrackedChanges counts the lines of the untracked files in paths. A
// file deleted since paths was listed is left out.
func (repo *Repo) untrackedChanges(paths []string) (changes []FileChange, err error) {
	if len(paths) == 0 {
		return nil, nil
	}
	root, err := os.OpenRoot(repo.Root)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	changes = make([]FileChange, 0, len(paths))
	for _, p := range paths {
		lines, binary, err := countWorktreeLines(root, p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
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

func countWorktreeLines(root *os.Root, path string) (lines int, binary bool, err error) {
	name := filepath.FromSlash(path)
	info, err := root.Lstat(name)
	if err != nil {
		return 0, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 1, false, nil
	}
	file, err := root.Open(name)
	if err != nil {
		return 0, false, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	br := bufio.NewReaderSize(file, binarySniffLen)
	head, err := br.Peek(binarySniffLen)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return 0, false, err
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return 0, true, nil
	}
	lines, err = countLines(br)
	return lines, false, err
}

// countLines matches git: a final line without a newline still counts.
func countLines(reader io.Reader) (int, error) {
	buf := make([]byte, 32*1024)
	lines := 0
	endsWithNewline := true
	for {
		count, err := reader.Read(buf)
		if count > 0 {
			lines += bytes.Count(buf[:count], []byte{'\n'})
			endsWithNewline = buf[count-1] == '\n'
		}
		if errors.Is(err, io.EOF) {
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
