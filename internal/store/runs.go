package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/psdcoder/agentdiff/internal/agent"
	"github.com/psdcoder/agentdiff/internal/gitx"
)

// MaxRuns is how many agent runs a review keeps logs of. Older logs go
// when a new run starts.
const MaxRuns = 50

const TargetOverview = "overview"

// RunInfo describes one agent run. Target is TargetOverview or the ID of
// the thread the run answered.
type RunInfo struct {
	ID      string         `json:"id"`
	Target  string         `json:"target"`
	Title   string         `json:"title"`
	Note    string         `json:"note,omitempty"`
	Agent   agent.Provider `json:"agent"`
	Model   string         `json:"model,omitempty"`
	Effort  string         `json:"effort,omitempty"`
	Started time.Time      `json:"started"`
	// Resumed is the session the run resumed or forked, empty for a new
	// session.
	Resumed string `json:"resumed,omitempty"`
	// Usage is what the run spent, known once it ended. It lives in a
	// file next to the log, so listing runs does not read whole logs.
	Usage *agent.Usage `json:"-"`
}

func NewRunID(t time.Time) string {
	return fmt.Sprintf("%s-%04x", t.Format("20060102-150405.000"), rand.N(1<<16))
}

func (s Store) runDir(repo string, scope gitx.Scope) string {
	return strings.TrimSuffix(s.reviewPath(repo, scope), ".json") + ".runs"
}

// RunPath is the file that holds the log of a run.
func (s Store) RunPath(repo string, scope gitx.Scope, id string) string {
	return filepath.Join(s.runDir(repo, scope), id+".jsonl")
}

func usagePath(log string) string { return strings.TrimSuffix(log, ".jsonl") + ".usage.json" }

func (s Store) SaveRunUsage(repo string, scope gitx.Scope, id string, u agent.Usage) error {
	return writeJSON(usagePath(s.RunPath(repo, scope, id)), u)
}

// RunWriter appends the log entries of one run to its file: the run's
// RunInfo on the first line, one agent.LogEntry on each line after it.
type RunWriter struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

func (s Store) CreateRun(repo string, scope gitx.Scope, info RunInfo) (*RunWriter, error) {
	dir := s.runDir(repo, scope)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s.pruneRuns(dir)
	f, err := os.OpenFile(s.RunPath(repo, scope, info.ID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	w := &RunWriter{f: f, enc: json.NewEncoder(f)}
	w.enc.SetEscapeHTML(false)
	if err := w.enc.Encode(info); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

func (w *RunWriter) Write(e agent.LogEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enc.Encode(e)
}

func (w *RunWriter) Close() error { return w.f.Close() }

// pruneRuns leaves room for one more run. Run IDs start with their time,
// so the names sort oldest first.
func (s Store) pruneRuns(dir string) {
	names := runFiles(dir)
	for len(names) >= MaxRuns {
		path := filepath.Join(dir, names[0])
		os.Remove(path)
		os.Remove(usagePath(path))
		names = names[1:]
	}
}

func runFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names
}

// Runs lists the runs of a review, newest first.
func (s Store) Runs(repo string, scope gitx.Scope) []RunInfo {
	dir := s.runDir(repo, scope)
	names := runFiles(dir)
	slices.Reverse(names)
	var runs []RunInfo
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := readRunInfo(path)
		if err != nil {
			continue
		}
		var u agent.Usage
		if readJSON(usagePath(path), &u) == nil {
			info.Usage = &u
		}
		runs = append(runs, info)
	}
	return runs
}

func readRunInfo(path string) (RunInfo, error) {
	var info RunInfo
	f, err := os.Open(path)
	if err != nil {
		return info, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	line, err := r.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return info, err
	}
	if err := json.Unmarshal(line, &info); err != nil {
		return info, err
	}
	if info.Resumed == "" {
		// A run logged before Resumed was kept names the session in its
		// command, the entry after the header.
		var cmd agent.LogEntry
		if line, err := r.ReadBytes('\n'); err == nil && json.Unmarshal(line, &cmd) == nil && cmd.Kind == agent.LogCommand {
			if m := resumeArg.FindStringSubmatch(cmd.Text); m != nil {
				info.Resumed = m[1]
			}
		}
	}
	return info, nil
}

// resumeArg finds the session in a command that resumes or forks one:
// Claude's --resume, Codex's resume and fork.
var resumeArg = regexp.MustCompile(`(?:--resume|\bresume|\bfork) ([0-9a-f]{8}-[0-9a-f-]{27})`)

// ReadRun returns the log entries of a run. A line cut off by a crash is
// skipped.
func (s Store) ReadRun(repo string, scope gitx.Scope, id string) ([]agent.LogEntry, error) {
	f, err := os.Open(s.RunPath(repo, scope, id))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	if _, err := r.ReadBytes('\n'); err != nil {
		return nil, nil
	}
	var entries []agent.LogEntry
	for {
		line, err := r.ReadBytes('\n')
		var e agent.LogEntry
		if len(line) > 0 && json.Unmarshal(line, &e) == nil {
			entries = append(entries, e)
		}
		if err == io.EOF {
			return entries, nil
		}
		if err != nil {
			return entries, err
		}
	}
}
