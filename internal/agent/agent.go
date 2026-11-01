package agent

import (
	"bufio"
	"cmp"
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
	// UserSetup lets the CLI load the user's own setup: instructions,
	// skills, hooks, plugins and MCP servers. Without it, a run gets only
	// the prompts of the app.
	UserSetup bool `json:"userSetup,omitempty"`
}

// Auto is the config for the runs the app starts on its own: the
// overview and the walkthroughs it writes ahead.
func (config Config) Auto() Config {
	config.Model, config.Effort = cmp.Or(config.AutoModel, config.Model), cmp.Or(config.AutoEffort, config.Effort)
	return config
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

func (event Event) terminal() bool {
	return event.Kind == EventDone || event.Kind == EventError
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

func newParser(provider Provider, dir string) lineParser { return specOf(provider).parser(dir) }

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

func forward(ctx context.Context, output io.Reader, parser lineParser, ch chan<- Event, log *logger) (bool, error) {
	scanner := bufio.NewScanner(output)
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

func failure(provider Provider, streamErr, exitErr error, stderr string) error {
	msg := fmt.Sprintf("%s exited without a result", provider)
	if exitErr != nil {
		msg = fmt.Sprintf("%s failed: %v", provider, exitErr)
	}
	if streamErr != nil {
		msg += ": " + streamErr.Error()
	}
	if stderrText := strings.TrimSpace(stderr); stderrText != "" {
		msg += "\n" + stderrText
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

// Args are the arguments of the CLI for a request, nil for an unknown
// provider.
func Args(cfg Config, req Request) []string {
	sp, ok := lookup(cfg.Provider)
	if !ok {
		return nil
	}
	return sp.args(cfg, req)
}

func stdinText(cfg Config, req Request) string { return specOf(cfg.Provider).stdin(req) }

type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (buffer *tailBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.buf = append(buffer.buf, data...)
	if over := len(buffer.buf) - buffer.max; over > 0 {
		buffer.buf = append(buffer.buf[:0], buffer.buf[over:]...)
	}
	return len(data), nil
}

func (buffer *tailBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.buf)
}
