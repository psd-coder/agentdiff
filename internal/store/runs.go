package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
)

// MaxRuns is how many agent runs a review keeps logs of. Older logs go
// when a new run starts.
const MaxRuns = 50

const TargetOverview = review.TargetOverview

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

func NewRunID(started time.Time) string {
	return fmt.Sprintf("%s-%04x", started.Format("20060102-150405.000"), rand.N(1<<16))
}

func (store Store) runDir(repo string, scope gitx.Scope) string {
	return strings.TrimSuffix(store.reviewPath(repo, scope), ".json") + ".runs"
}

// RunPath is the file that holds the log of a run.
func (store Store) RunPath(repo string, scope gitx.Scope, id string) string {
	return filepath.Join(store.runDir(repo, scope), id+".jsonl")
}

func usagePath(log string) string { return strings.TrimSuffix(log, ".jsonl") + ".usage.json" }

// RemoveRun deletes the log of a run.
func (store Store) RemoveRun(repo string, scope gitx.Scope, id string) error {
	return os.Remove(store.RunPath(repo, scope, id))
}

func (store Store) SaveRunUsage(repo string, scope gitx.Scope, id string, usage agent.Usage) error {
	return writeJSON(usagePath(store.RunPath(repo, scope, id)), usage)
}

// RunWriter appends the log entries of one run to its file: the run's
// RunInfo on the first line, one agent.LogEntry on each line after it.
type RunWriter struct {
	mu      sync.Mutex
	logFile *os.File
	enc     *json.Encoder
}

func (store Store) CreateRun(repo string, scope gitx.Scope, info RunInfo) (*RunWriter, error) {
	dir := store.runDir(repo, scope)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	store.pruneRuns(dir)
	file, err := os.OpenFile(store.RunPath(repo, scope, info.ID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	writer := &RunWriter{logFile: file, enc: json.NewEncoder(file)}
	writer.enc.SetEscapeHTML(false)
	if err := writer.enc.Encode(info); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return writer, nil
}

func (writer *RunWriter) Write(entry agent.LogEntry) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.enc.Encode(entry)
}

func (writer *RunWriter) Close() error { return writer.logFile.Close() }

// pruneRuns leaves room for one more run. Run IDs start with their time,
// so the names sort oldest first.
func (store Store) pruneRuns(dir string) {
	names := runFiles(dir)
	for len(names) >= MaxRuns {
		path := filepath.Join(dir, names[0])
		for _, p := range []string{path, usagePath(path)} {
			if err := os.Remove(p); err != nil && !IsNotExist(err) {
				log.Printf("prune runs: %v", err)
			}
		}
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
func (store Store) Runs(repo string, scope gitx.Scope) []RunInfo {
	dir := store.runDir(repo, scope)
	names := runFiles(dir)
	slices.Reverse(names)
	var runs []RunInfo
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := readRunInfo(path)
		if err != nil {
			continue
		}
		var usage agent.Usage
		if readJSON(usagePath(path), &usage) == nil {
			info.Usage = &usage
		}
		runs = append(runs, info)
	}
	return runs
}

func readRunInfo(path string) (info RunInfo, err error) {
	file, err := os.Open(path)
	if err != nil {
		return info, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	reader := bufio.NewReader(file)
	line, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return info, err
	}
	if err := json.Unmarshal(line, &info); err != nil {
		return info, err
	}
	if info.Resumed == "" {
		// A run logged before Resumed was kept names the session in its
		// command, the entry after the header.
		var cmd agent.LogEntry
		if line, err := reader.ReadBytes('\n'); err == nil && json.Unmarshal(line, &cmd) == nil && cmd.Kind == agent.LogCommand {
			info.Resumed = agent.ResumedSession(info.Agent, cmd.Text)
		}
	}
	return info, nil
}

// ReadRun returns the log entries of a run. A line cut off by a crash is
// skipped.
func (store Store) ReadRun(repo string, scope gitx.Scope, id string) (entries []agent.LogEntry, err error) {
	file, err := os.Open(store.RunPath(repo, scope, id))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	reader := bufio.NewReader(file)
	if _, err := reader.ReadBytes('\n'); err != nil {
		return nil, nil
	}
	for {
		line, err := reader.ReadBytes('\n')
		var entry agent.LogEntry
		if len(line) > 0 && json.Unmarshal(line, &entry) == nil {
			entries = append(entries, entry)
		}
		if errors.Is(err, io.EOF) {
			return entries, nil
		}
		if err != nil {
			return entries, err
		}
	}
}
