package gitx

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const maxContentSize = 2 << 20

func checkFile(fileChange FileChange) error {
	if fileChange.Binary {
		return ErrBinary
	}
	if err := checkPath(fileChange.Path); err != nil {
		return err
	}
	if fileChange.OldPath != "" {
		return checkPath(fileChange.OldPath)
	}
	return nil
}

// sides reads the old and the new side of f between the trees of v.
func (repo *Repo) sides(ctx context.Context, revisions revs, fileChange FileChange, blob func(rev, path string) (string, error)) (old, new string, err error) {
	if fileChange.Status != StatusAdded {
		if old, err = blob(revisions.from, cmp.Or(fileChange.OldPath, fileChange.Path)); err != nil {
			return "", "", err
		}
	}
	if fileChange.Status != StatusDeleted {
		if new, err = repo.newSide(ctx, revisions, fileChange.Path, blob); err != nil {
			return "", "", err
		}
	}
	return old, new, nil
}

// newSide reads path as it is at the end of v.
func (repo *Repo) newSide(ctx context.Context, revisions revs, path string, blob func(rev, path string) (string, error)) (string, error) {
	if revisions.worktree() {
		return repo.worktreeFile(ctx, path)
	}
	return blob(revisions.to, path)
}

func checkPath(path string) error {
	if !validPath(path) {
		return fmt.Errorf("%w: %q", ErrInvalidPath, path)
	}
	return nil
}

func validPath(path string) bool {
	return path != "" && !strings.ContainsRune(path, 0) && filepath.IsLocal(filepath.FromSlash(path))
}

func (repo *Repo) blob(ctx context.Context, rev, path string) (string, error) {
	spec := rev + ":" + path
	oid, kind, size, err := repo.objectInfo(ctx, spec)
	if err != nil {
		return "", err
	}
	if kind == "commit" {
		return submoduleText(oid), nil
	}
	if size > maxContentSize {
		return "", ErrTooLarge
	}
	out, err := repo.output(ctx, "cat-file", "blob", oid)
	if err != nil {
		return "", err
	}
	return text(out)
}

// submoduleText is how git diff shows a submodule, which a tree stores
// as a commit entry.
func submoduleText(oid string) string { return "Subproject commit " + oid + "\n" }

func (repo *Repo) objectInfo(ctx context.Context, spec string) (oid, kind string, size int64, err error) {
	oid, ok, err := repo.optionalLine(ctx, "rev-parse", "-q", "--verify", spec)
	if err != nil {
		return "", "", 0, err
	}
	if !ok {
		return "", "", 0, fmt.Errorf("gitx: %s does not exist", spec)
	}
	var out bytes.Buffer
	stdin := strings.NewReader(oid + "\n")
	err = runIn(ctx, repo.Root, stdin, &out, "cat-file", "--batch-check=%(objecttype) %(objectsize)")
	if err != nil {
		return "", "", 0, err
	}
	kind, sizeText, found := strings.Cut(strings.TrimSpace(out.String()), " ")
	if !found {
		return "", "", 0, fmt.Errorf("gitx: %s: %s", spec, strings.TrimSpace(out.String()))
	}
	// The tree has the entry, but its object is not in the repository:
	// it is the commit of a submodule, which lives in the submodule.
	if sizeText == "missing" {
		return oid, "commit", 0, nil
	}
	size, err = strconv.ParseInt(sizeText, 10, 64)
	return oid, kind, size, err
}

// worktreeFile reads the file through an os.Root, so a symlinked directory
// in the path cannot lead outside the repository.
func (repo *Repo) worktreeFile(ctx context.Context, path string) (content string, err error) {
	root, err := os.OpenRoot(repo.Root)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	name := filepath.FromSlash(path)
	info, err := root.Lstat(name)
	if err != nil {
		return "", err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return root.Readlink(name)
	case info.IsDir():
		var out bytes.Buffer
		if err := runIn(ctx, filepath.Join(repo.Root, name), nil, &out, "rev-parse", "HEAD"); err != nil {
			return "", err
		}
		return submoduleText(strings.TrimSpace(out.String())), nil
	case info.Size() > maxContentSize:
		return "", ErrTooLarge
	}
	file, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
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

var catFileArgs = []string{"cat-file", "--batch-command"}

var errReaderClosed = errors.New("gitx: content reader is closed")

// ContentReader reads the files of one snapshot. It reads every blob
// through one git cat-file process, so a file costs no new git process.
// When that process cannot start, it starts git for each blob instead.
// It is not safe for concurrent use.
type ContentReader struct {
	repo *Repo
	// ctx is the caller's context: the process stops when it is done.
	ctx  context.Context
	stop context.CancelFunc
	revs revs
	// cmd is nil when the reader starts git for each blob.
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	stderr bytes.Buffer
	// err is set once the process is gone, and every later call returns it.
	err error
}

// NewContentReader starts the git process that reads the files of the
// snapshot, which runs until Close or until ctx is done. The caller must
// call Close.
func (snap *Snapshot) NewContentReader(ctx context.Context) *ContentReader {
	reader := &ContentReader{repo: snap.repo, ctx: ctx, revs: snap.revs}
	if err := reader.start(); err != nil {
		reader.cmd = nil
	}
	return reader
}

func (reader *ContentReader) start() error {
	procCtx, stop := context.WithCancel(reader.ctx)
	reader.stop = stop
	reader.cmd = command(procCtx, reader.repo.Root, catFileArgs...)
	reader.cmd.Stderr = &reader.stderr
	var err error
	if reader.stdin, err = reader.cmd.StdinPipe(); err != nil {
		stop()
		return err
	}
	stdout, err := reader.cmd.StdoutPipe()
	if err != nil {
		stop()
		return errors.Join(err, reader.stdin.Close())
	}
	if err := reader.cmd.Start(); err != nil {
		stop()
		return err
	}
	reader.out = bufio.NewReader(stdout)
	// A git older than 2.36 has no --batch-command and exits at once,
	// which the first request finds.
	if _, _, _, err := reader.info(reader.revs.from); err != nil {
		return errors.Join(err, reader.Close())
	}
	return nil
}

// Contents is Snapshot.Contents through the reader.
func (reader *ContentReader) Contents(fileChange FileChange) (old, new string, err error) {
	if err := checkFile(fileChange); err != nil {
		return "", "", err
	}
	return reader.repo.sides(reader.ctx, reader.revs, fileChange, reader.blob)
}

// File is Snapshot.File through the reader.
func (reader *ContentReader) File(path string) (string, error) {
	if err := checkPath(path); err != nil {
		return "", err
	}
	return reader.repo.newSide(reader.ctx, reader.revs, path, reader.blob)
}

// Close stops the git process and waits for it to exit.
func (reader *ContentReader) Close() error {
	if reader.cmd == nil || reader.err != nil {
		return nil
	}
	reader.err = errReaderClosed
	stdinErr := reader.stdin.Close()
	err := reader.cmd.Wait()
	reader.stop()
	if err != nil {
		if cerr := reader.ctx.Err(); cerr != nil {
			return cerr
		}
		return &Error{Args: catFileArgs, Stderr: strings.TrimSpace(reader.stderr.String()), Err: err}
	}
	return stdinErr
}

func (reader *ContentReader) blob(rev, path string) (string, error) {
	// A request is one line, and git drops a CR at its end.
	if reader.cmd == nil || strings.ContainsAny(path, "\n\r") {
		return reader.repo.blob(reader.ctx, rev, path)
	}
	spec := rev + ":" + path
	oid, kind, size, err := reader.info(spec)
	switch {
	case err != nil:
		return "", err
	case kind == "commit":
		return submoduleText(oid), nil
	case kind != "blob":
		return "", fmt.Errorf("gitx: %s is a %s", spec, kind)
	case size > maxContentSize:
		return "", ErrTooLarge
	}
	header, err := reader.request("contents " + oid)
	if err != nil {
		return "", err
	}
	if header != oid+" blob "+strconv.FormatInt(size, 10) {
		return "", reader.fail(fmt.Errorf("unexpected reply %q", header))
	}
	// The contents end with a newline of their own.
	data := make([]byte, size+1)
	if _, err := io.ReadFull(reader.out, data); err != nil {
		return "", reader.fail(err)
	}
	if data[size] != '\n' {
		return "", reader.fail(fmt.Errorf("no newline after %s", oid))
	}
	return text(data[:size])
}

// info asks for the type and size of an object. Git answers
// "<oid> <type> <size>", "<spec> missing", or "<oid> submodule" for a
// submodule commit that is not in the repository. The spec can hold
// spaces, so the last word tells the answers apart.
func (reader *ContentReader) info(spec string) (oid, kind string, size int64, err error) {
	header, err := reader.request("info " + spec)
	if err != nil {
		return "", "", 0, err
	}
	lastSpace := strings.LastIndexByte(header, ' ')
	rest, last := header[:max(lastSpace, 0)], header[lastSpace+1:]
	switch last {
	case "missing":
		// Git before 2.51 also answers "missing" for a submodule commit
		// that is not in the repository. Rare, so it can cost a process.
		return reader.repo.objectInfo(reader.ctx, spec)
	case "submodule":
		return rest, "commit", 0, nil
	}
	oid, kind, found := strings.Cut(rest, " ")
	size, perr := strconv.ParseInt(last, 10, 64)
	if !found || perr != nil {
		return "", "", 0, fmt.Errorf("gitx: %s: %s", spec, header)
	}
	return oid, kind, size, nil
}

// request sends one command and reads the first line of the reply.
func (reader *ContentReader) request(cmd string) (string, error) {
	if reader.err != nil {
		return "", reader.err
	}
	if err := reader.ctx.Err(); err != nil {
		return "", reader.fail(err)
	}
	if _, err := io.WriteString(reader.stdin, cmd+"\n"); err != nil {
		return "", reader.fail(err)
	}
	line, err := reader.out.ReadString('\n')
	if err != nil {
		return "", reader.fail(err)
	}
	return strings.TrimSuffix(line, "\n"), nil
}

// fail stops the process after a broken reply, because the next read
// could start in the middle of it.
func (reader *ContentReader) fail(err error) error {
	if reader.err != nil {
		return reader.err
	}
	stdinErr := reader.stdin.Close()
	reader.stop()
	// The process was just killed, so Wait only reports the kill.
	_ = reader.cmd.Wait()
	if cerr := reader.ctx.Err(); cerr != nil {
		reader.err = cerr
	} else {
		reader.err = &Error{Args: catFileArgs, Stderr: strings.TrimSpace(reader.stderr.String()), Err: errors.Join(err, stdinErr)}
	}
	return reader.err
}
