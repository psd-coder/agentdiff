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

func (r *Repo) Contents(ctx context.Context, s Scope, f FileChange) (old, new string, err error) {
	if err := checkFile(f); err != nil {
		return "", "", err
	}
	v, err := r.resolve(ctx, s)
	if err != nil {
		return "", "", err
	}
	return r.sides(ctx, v, f, func(rev, path string) (string, error) {
		return r.blob(ctx, rev, path)
	})
}

func checkFile(f FileChange) error {
	if f.Binary {
		return ErrBinary
	}
	if !validPath(f.Path) || (f.OldPath != "" && !validPath(f.OldPath)) {
		return fmt.Errorf("%w: %q", ErrInvalidPath, f.Path)
	}
	return nil
}

// sides reads the old and the new side of f between the trees of v.
func (r *Repo) sides(ctx context.Context, v revs, f FileChange, blob func(rev, path string) (string, error)) (old, new string, err error) {
	if f.Status != StatusAdded {
		if old, err = blob(v.from, cmp.Or(f.OldPath, f.Path)); err != nil {
			return "", "", err
		}
	}
	if f.Status != StatusDeleted {
		if v.worktree() {
			new, err = r.worktreeFile(ctx, f.Path)
		} else {
			new, err = blob(v.to, f.Path)
		}
		if err != nil {
			return "", "", err
		}
	}
	return old, new, nil
}

func validPath(p string) bool {
	return p != "" && !strings.ContainsRune(p, 0) && filepath.IsLocal(filepath.FromSlash(p))
}

func (r *Repo) blob(ctx context.Context, rev, path string) (string, error) {
	spec := rev + ":" + path
	oid, kind, size, err := r.objectInfo(ctx, spec)
	if err != nil {
		return "", err
	}
	if kind == "commit" {
		return submoduleText(oid), nil
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

// submoduleText is how git diff shows a submodule, which a tree stores
// as a commit entry.
func submoduleText(oid string) string { return "Subproject commit " + oid + "\n" }

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
func (r *Repo) worktreeFile(ctx context.Context, path string) (content string, err error) {
	root, err := os.OpenRoot(r.Root)
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
		if err := runIn(ctx, filepath.Join(r.Root, name), nil, &out, "rev-parse", "HEAD"); err != nil {
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

// ContentReader reads the files of one scope. It resolves the scope once
// and reads every blob through one git cat-file process, so a file costs
// no new git process. It is not safe for concurrent use.
type ContentReader struct {
	repo *Repo
	// ctx is the caller's context: the process stops when it is done.
	ctx    context.Context
	stop   context.CancelFunc
	revs   revs
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	stderr bytes.Buffer
	// err is set once the process is gone, and every later call returns it.
	err error
}

// NewContentReader resolves s and starts the git process, which runs
// until Close or until ctx is done. The caller must call Close.
func (r *Repo) NewContentReader(ctx context.Context, s Scope) (*ContentReader, error) {
	v, err := r.resolve(ctx, s)
	if err != nil {
		return nil, err
	}
	procCtx, stop := context.WithCancel(ctx)
	c := &ContentReader{repo: r, ctx: ctx, stop: stop, revs: v}
	c.cmd = command(procCtx, r.Root, catFileArgs...)
	c.cmd.Stderr = &c.stderr
	if c.stdin, err = c.cmd.StdinPipe(); err != nil {
		stop()
		return nil, err
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		err = errors.Join(err, c.stdin.Close())
		stop()
		return nil, err
	}
	if err := c.cmd.Start(); err != nil {
		stop()
		return nil, &Error{Args: catFileArgs, Err: err}
	}
	c.out = bufio.NewReader(stdout)
	// A git older than 2.36 has no --batch-command and exits at once.
	// The first request finds that, so the caller can use Contents instead.
	if _, _, _, err := c.info(v.from); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	return c, nil
}

// Contents is Repo.Contents for one file of the reader's scope.
func (c *ContentReader) Contents(f FileChange) (old, new string, err error) {
	if err := checkFile(f); err != nil {
		return "", "", err
	}
	return c.repo.sides(c.ctx, c.revs, f, c.blob)
}

// Close stops the git process and waits for it to exit.
func (c *ContentReader) Close() error {
	if c.err != nil {
		return nil
	}
	c.err = errReaderClosed
	stdinErr := c.stdin.Close()
	err := c.cmd.Wait()
	c.stop()
	if err != nil {
		if cerr := c.ctx.Err(); cerr != nil {
			return cerr
		}
		return &Error{Args: catFileArgs, Stderr: strings.TrimSpace(c.stderr.String()), Err: err}
	}
	return stdinErr
}

func (c *ContentReader) blob(rev, path string) (string, error) {
	// A request is one line, and git drops a CR at its end.
	if strings.ContainsAny(path, "\n\r") {
		return c.repo.blob(c.ctx, rev, path)
	}
	spec := rev + ":" + path
	oid, kind, size, err := c.info(spec)
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
	header, err := c.request("contents " + oid)
	if err != nil {
		return "", err
	}
	if header != oid+" blob "+strconv.FormatInt(size, 10) {
		return "", c.fail(fmt.Errorf("unexpected reply %q", header))
	}
	// The contents end with a newline of their own.
	data := make([]byte, size+1)
	if _, err := io.ReadFull(c.out, data); err != nil {
		return "", c.fail(err)
	}
	if data[size] != '\n' {
		return "", c.fail(fmt.Errorf("no newline after %s", oid))
	}
	return text(data[:size])
}

// info asks for the type and size of an object. Git answers
// "<oid> <type> <size>", "<spec> missing", or "<oid> submodule" for a
// submodule commit that is not in the repository. The spec can hold
// spaces, so the last word tells the answers apart.
func (c *ContentReader) info(spec string) (oid, kind string, size int64, err error) {
	header, err := c.request("info " + spec)
	if err != nil {
		return "", "", 0, err
	}
	i := strings.LastIndexByte(header, ' ')
	rest, last := header[:max(i, 0)], header[i+1:]
	switch last {
	case "missing":
		// Git before 2.51 also answers "missing" for a submodule commit
		// that is not in the repository. Rare, so it can cost a process.
		return c.repo.objectInfo(c.ctx, spec)
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
func (c *ContentReader) request(cmd string) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	if err := c.ctx.Err(); err != nil {
		return "", c.fail(err)
	}
	if _, err := io.WriteString(c.stdin, cmd+"\n"); err != nil {
		return "", c.fail(err)
	}
	line, err := c.out.ReadString('\n')
	if err != nil {
		return "", c.fail(err)
	}
	return strings.TrimSuffix(line, "\n"), nil
}

// fail stops the process after a broken reply, because the next read
// could start in the middle of it.
func (c *ContentReader) fail(err error) error {
	if c.err != nil {
		return c.err
	}
	stdinErr := c.stdin.Close()
	c.stop()
	// The process was just killed, so Wait only reports the kill.
	_ = c.cmd.Wait()
	if cerr := c.ctx.Err(); cerr != nil {
		c.err = cerr
	} else {
		c.err = &Error{Args: catFileArgs, Stderr: strings.TrimSpace(c.stderr.String()), Err: errors.Join(err, stdinErr)}
	}
	return c.err
}
