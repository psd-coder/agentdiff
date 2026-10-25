// Package gitx reads review scopes from a git repository through the git CLI.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

var (
	ErrBinary      = errors.New("gitx: binary file")
	ErrTooLarge    = errors.New("gitx: file too large")
	ErrNotRepo     = errors.New("gitx: not a git repository")
	ErrInvalidPath = errors.New("gitx: invalid path")
)

// Error is a failed git invocation with its stderr.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := e.Stderr
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", e.Args[0], msg)
}

func (e *Error) Unwrap() error { return e.Err }

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

var gitEnv = []string{
	"GIT_OPTIONAL_LOCKS=0",
	"LC_ALL=C",
	"GIT_TERMINAL_PROMPT=0",
	"GIT_PAGER=cat",
}

func command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), gitEnv...)
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

func runIn(ctx context.Context, dir string, stdin io.Reader, stdout io.Writer, args ...string) error {
	cmd := command(ctx, dir, args...)
	var stderr bytes.Buffer
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return &Error{Args: args, Stderr: strings.TrimSpace(stderr.String()), Err: err}
	}
	return nil
}

func (r *Repo) run(ctx context.Context, stdout io.Writer, args ...string) error {
	return runIn(ctx, r.Root, nil, stdout, args...)
}

func (r *Repo) output(ctx context.Context, args ...string) ([]byte, error) {
	var out bytes.Buffer
	err := r.run(ctx, &out, args...)
	return out.Bytes(), err
}

func (r *Repo) line(ctx context.Context, args ...string) (string, error) {
	out, err := r.output(ctx, args...)
	return strings.TrimSpace(string(out)), err
}

// optionalLine treats exit code 1 as "no value", which is how `-q` queries report absence.
func (r *Repo) optionalLine(ctx context.Context, args ...string) (string, bool, error) {
	out, err := r.line(ctx, args...)
	if exitCode(err) == 1 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return out, true, nil
}

type cappedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

// Write never fails so git keeps running to completion; the excess is dropped.
func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.max <= 0 {
		return c.buf.Write(p)
	}
	room := c.max - c.buf.Len()
	if len(p) > room {
		c.truncated = true
		if room > 0 {
			c.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) full() bool { return c.max > 0 && c.buf.Len() >= c.max }

func (c *cappedBuffer) String() string {
	if !c.truncated {
		return c.buf.String()
	}
	b := c.buf.Bytes()
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		b = b[:i+1]
	}
	return string(b)
}
