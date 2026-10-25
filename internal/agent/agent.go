package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Provider string

const (
	Claude Provider = "claude"
	Codex  Provider = "codex"
)

type Config struct {
	Provider  Provider `json:"provider"`
	Model     string   `json:"model,omitempty"`
	Binary    string   `json:"binary,omitempty"`
	ExtraArgs []string `json:"extraArgs,omitempty"`
}

type Request struct {
	Dir          string
	Prompt       string
	SystemPrompt string
	ResumeID     string
	Fork         bool
}

type EventKind int

const (
	EventSession EventKind = iota
	EventText
	EventActivity
	EventDone
	EventError
)

type Event struct {
	Kind      EventKind
	Text      string
	SessionID string
	Err       error
}

func (e Event) terminal() bool {
	return e.Kind == EventDone || e.Kind == EventError
}

const (
	maxLineSize    = 64 << 20
	stderrTailSize = 2048
	waitDelay      = 3 * time.Second
	versionTimeout = 5 * time.Second
)

type lineParser interface {
	parse(line []byte) []Event
	streamError() error
}

func newParser(p Provider, dir string) lineParser {
	if p == Codex {
		return &codexParser{}
	}
	return &claudeParser{dir: dir}
}

func Run(ctx context.Context, cfg Config, req Request) <-chan Event {
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		run(ctx, cfg, req, ch)
	}()
	return ch
}

func run(ctx context.Context, cfg Config, req Request, ch chan<- Event) {
	env := loginEnv()
	bin, err := resolveBinary(cfg, env)
	if err != nil {
		ch <- Event{Kind: EventError, Err: err}
		return
	}

	stderr := &tailBuffer{max: stderrTailSize}
	pr, pw := io.Pipe()
	cmd := exec.CommandContext(ctx, bin, Args(cfg, req)...)
	cmd.Dir = req.Dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdinText(cfg, req))
	cmd.Stdout = pw
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = waitDelay

	if err := cmd.Start(); err != nil {
		ch <- Event{Kind: EventError, Err: firstErr(ctx.Err(), err)}
		return
	}
	waitErr := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		pw.Close()
		waitErr <- err
	}()

	parser := newParser(cfg.Provider, req.Dir)
	done := forward(ctx, pr, parser, ch)
	_, _ = io.Copy(io.Discard, pr)
	exitErr := <-waitErr
	if done {
		return
	}
	if ctx.Err() != nil {
		ch <- Event{Kind: EventError, Err: ctx.Err()}
		return
	}
	ch <- Event{Kind: EventError, Err: failure(cfg.Provider, parser.streamError(), exitErr, stderr.String())}
}

func forward(ctx context.Context, r io.Reader, parser lineParser, ch chan<- Event) bool {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineSize)
	for scanner.Scan() {
		for _, ev := range parser.parse(scanner.Bytes()) {
			if ev.terminal() {
				ch <- ev
				return true
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
			}
		}
	}
	return false
}

func failure(p Provider, streamErr, exitErr error, stderr string) error {
	msg := fmt.Sprintf("%s exited without a result", p)
	if exitErr != nil {
		msg = fmt.Sprintf("%s failed: %v", p, exitErr)
	}
	if streamErr != nil {
		msg += ": " + streamErr.Error()
	}
	if s := strings.TrimSpace(stderr); s != "" {
		msg += "\n" + s
	}
	return errors.New(msg)
}

// killGroup kills the process and its children. A child forked while the
// signal is delivered can miss it on macOS and keep stdout open, so the
// kill repeats until the group is gone.
func killGroup(cmd *exec.Cmd) error {
	pgid := -cmd.Process.Pid
	err := syscall.Kill(pgid, syscall.SIGKILL)
	go func() {
		for range 20 {
			time.Sleep(50 * time.Millisecond)
			if syscall.Kill(pgid, syscall.SIGKILL) != nil {
				return
			}
		}
	}()
	return err
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func Available(ctx context.Context, cfg Config) (path, version string, err error) {
	env := loginEnv()
	path, err = resolveBinary(cfg, env)
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	version = strings.TrimSpace(string(out))
	if err != nil {
		return path, version, fmt.Errorf("%s --version: %w", path, firstErr(ctx.Err(), err))
	}
	return path, version, nil
}

func Args(cfg Config, req Request) []string {
	switch cfg.Provider {
	case Claude:
		return claudeArgs(cfg, req)
	case Codex:
		return codexArgs(cfg, req)
	}
	return nil
}

var readOnlyCommands = []string{
	"git diff", "git show", "git log", "git status", "git blame", "git ls-files", "git rev-parse",
	"grep", "rg", "ls", "cat", "head", "tail", "wc",
}

// readOnlyRules allows each command also with an rtk prefix: rtk's hook
// rewrites commands before Claude checks its rules.
func readOnlyRules() []string {
	rules := make([]string, 0, 2*len(readOnlyCommands))
	for _, c := range readOnlyCommands {
		rules = append(rules, "Bash("+c+":*)", "Bash(rtk "+c+":*)")
	}
	return rules
}

// claudeArgs keeps Claude read-only: in dontAsk mode a tool call that no
// allow rule matches is refused instead of asked about, so the allow list
// below, plus the user's own rules, is all that Bash may run.
func claudeArgs(cfg Config, req Request) []string {
	args := []string{
		"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--tools", "Read,Grep,Glob,Bash",
		"--permission-mode", "dontAsk",
		"--allowedTools",
	}
	args = append(args, readOnlyRules()...)
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if req.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", req.SystemPrompt)
	}
	if req.ResumeID != "" {
		args = append(args, "--resume", req.ResumeID)
		if req.Fork {
			args = append(args, "--fork-session")
		}
	}
	return append(args, cfg.ExtraArgs...)
}

func codexArgs(cfg Config, req Request) []string {
	args := []string{"exec", "--json", "-s", "read-only", "--skip-git-repo-check"}
	if req.Dir != "" {
		args = append(args, "-C", req.Dir)
	}
	if cfg.Model != "" {
		args = append(args, "-m", cfg.Model)
	}
	args = append(args, cfg.ExtraArgs...)
	switch {
	case req.ResumeID != "" && req.Fork:
		args = append(args, "fork", req.ResumeID)
	case req.ResumeID != "":
		args = append(args, "resume", req.ResumeID)
	}
	return append(args, "-")
}

func stdinText(cfg Config, req Request) string {
	if cfg.Provider != Codex || req.SystemPrompt == "" {
		return req.Prompt
	}
	return "<instructions>\n" + req.SystemPrompt + "\n</instructions>\n\n" + req.Prompt
}

type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.max; over > 0 {
		b.buf = append(b.buf[:0], b.buf[over:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
