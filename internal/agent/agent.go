package agent

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
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

// Config is how to run an agent. AutoModel and AutoEffort replace Model
// and Effort for the runs the app starts on its own; empty keeps them.
// AutoEffort has no omitempty, so an empty one stays empty instead of
// getting the default again.
type Config struct {
	Provider   Provider `json:"provider"`
	Model      string   `json:"model,omitempty"`
	Effort     string   `json:"effort,omitempty"`
	AutoModel  string   `json:"autoModel,omitempty"`
	AutoEffort string   `json:"autoEffort"`
	Binary     string   `json:"binary,omitempty"`
	ExtraArgs  []string `json:"extraArgs,omitempty"`
}

// Auto is the config for the runs the app starts on its own: the
// overview and the walkthroughs it writes ahead.
func (c Config) Auto() Config {
	c.Model, c.Effort = cmp.Or(c.AutoModel, c.Model), cmp.Or(c.AutoEffort, c.Effort)
	return c
}

// Efforts lists the effort levels the CLI of a provider takes.
func Efforts(p Provider) []string {
	if p == Codex {
		return []string{"minimal", "low", "medium", "high", "xhigh"}
	}
	return []string{"low", "medium", "high", "xhigh", "max"}
}

type Request struct {
	Dir          string
	Prompt       string
	SystemPrompt string
	ResumeID     string
	Fork         bool
	// Log receives the run as it happens: the command, the prompt, every
	// line of output and the exit. Calls never overlap.
	Log func(LogEntry)
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
	log := newLogger(req.Log)
	env := loginEnv()
	bin, err := resolveBinary(cfg, env)
	if err != nil {
		log.add(LogExit, "Could not start: "+err.Error())
		ch <- Event{Kind: EventError, Err: err}
		return
	}

	args := Args(cfg, req)
	stdin := stdinText(cfg, req)
	log.add(LogCommand, commandLine(req.Dir, bin, args))
	log.add(LogStdin, stdin)
	stderr := &tailBuffer{max: stderrTailSize}
	stderrLines := &lineWriter{emit: func(line string) { log.add(LogStderr, line) }}
	pr, pw := io.Pipe()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = req.Dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = pw
	cmd.Stderr = io.MultiWriter(stderr, stderrLines)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = waitDelay

	if err := cmd.Start(); err != nil {
		err = firstErr(ctx.Err(), err)
		log.add(LogExit, "Could not start: "+err.Error())
		ch <- Event{Kind: EventError, Err: err}
		return
	}
	waitErr := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		// Close of an io.PipeWriter always returns nil.
		_ = pw.Close()
		waitErr <- err
	}()

	parser := newParser(cfg.Provider, req.Dir)
	done, readErr := forward(ctx, pr, parser, ch, log)
	_, _ = io.Copy(io.Discard, pr)
	exitErr := <-waitErr
	stderrLines.flush()
	log.add(LogExit, exitText(ctx, exitErr, done))
	if done {
		return
	}
	if ctx.Err() != nil {
		ch <- Event{Kind: EventError, Err: ctx.Err()}
		return
	}
	streamErr := errors.Join(parser.streamError(), readErr)
	ch <- Event{Kind: EventError, Err: failure(cfg.Provider, streamErr, exitErr, stderr.String())}
}

func forward(ctx context.Context, r io.Reader, parser lineParser, ch chan<- Event, log *logger) (bool, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineSize)
	for scanner.Scan() {
		log.add(LogStdout, scanner.Text())
		for _, ev := range parser.parse(scanner.Bytes()) {
			if ev.terminal() {
				ch <- ev
				return true, nil
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("read output: %w", err)
	}
	return false, nil
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
	if cfg.Effort != "" {
		args = append(args, "--effort", cfg.Effort)
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
	if cfg.Effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+strconv.Quote(cfg.Effort))
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
