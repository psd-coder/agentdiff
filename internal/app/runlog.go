package app

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

// logRun is one agent run in the log panel. Items of a past run are read
// from its file when it is first opened.
type logRun struct {
	info    store.RunInfo
	items   []logItem
	loaded  bool
	loading bool
	running bool
	open    bool
	err     string
	end     agent.RunStatus
}

type logItem struct {
	time time.Time
	agent.LogItem
	expanded bool
}

func logItems(provider agent.Provider, entry agent.LogEntry) []logItem {
	var items []logItem
	for _, it := range agent.Summarize(provider, entry) {
		items = append(items, logItem{time: entry.Time, LogItem: it})
	}
	return items
}

// add appends items to the run and reads how it ended from all its
// items. It returns what they report as spent, nil when none does.
func (logRun *logRun) add(items []logItem) *agent.Usage {
	logRun.items = append(logRun.items, items...)
	all := make([]agent.LogItem, len(logRun.items))
	for i, it := range logRun.items {
		all[i] = it.LogItem
	}
	logRun.end = agent.Status(all)
	return agent.SumUsage(all)
}

func (logRun *logRun) status() string {
	if logRun.running {
		return "running"
	}
	switch logRun.end {
	case agent.RunFailed:
		return "failed"
	case agent.RunStopped:
		return "stopped"
	case agent.RunUnknown, agent.RunDone:
	}
	return ""
}

// runCost is what run r spent itself. The run it resumed is looked up in
// runs.
func runCost(runs []*logRun, logRun *logRun) float64 {
	var parent *agent.Usage
	if resumed := resumedRun(runs, logRun); resumed != nil {
		parent = resumed.info.Usage
	}
	return agent.OwnCost(*logRun.info.Usage, logRun.info.Resumed != "", parent)
}

// fillCosts works out the own cost of the runs that only know the cost of
// their session. A run read from its log without a usage file is one, and
// so is a run whose resumed run had no usage when it ended.
func fillCosts(runs []*logRun) {
	for _, r := range runs {
		if usage := r.info.Usage; usage != nil && !r.running && usage.CostUSD == 0 && usage.SessionCost > 0 {
			usage.CostUSD = runCost(runs, r)
		}
	}
}

func (logRun *logRun) label() string {
	parts := []string{logRun.info.Title, logRun.info.Agent.Name()}
	if logRun.info.Model != "" {
		parts = append(parts, logRun.info.Model)
	}
	if logRun.info.Effort != "" {
		parts = append(parts, logRun.info.Effort+" effort")
	}
	parts = append(parts, logRun.info.Started.Format("Jan 2 15:04:05"))
	if usage := logRun.info.Usage; usage != nil {
		parts = append(parts, usage.Short())
	}
	if status := logRun.status(); status != "" {
		parts = append(parts, status)
	}
	return strings.Join(parts, " · ")
}

// text is the run as plain text, for the clipboard.
func (logRun *logRun) text() string {
	var text strings.Builder
	text.WriteString(logRun.label())
	text.WriteString("\n")
	if logRun.info.Note != "" {
		text.WriteString(logRun.info.Note)
		text.WriteString("\n")
	}
	for _, it := range logRun.items {
		fmt.Fprintf(&text, "\n[%s] %s\n", it.time.Format("15:04:05"), it.Title)
		if it.Detail != "" {
			text.WriteString(it.Detail)
			text.WriteString("\n")
		}
	}
	return text.String()
}

// logsOf returns the runs of a review, newest first, reading the list
// from the store the first time.
func (app *app) logsOf(rev *review.Review) []*logRun {
	if runs, ok := app.logs[rev]; ok {
		return runs
	}
	var runs []*logRun
	for _, info := range app.store.Runs(rev.Repo, rev.Scope) {
		runs = append(runs, &logRun{info: info})
	}
	app.logs[rev] = runs
	return runs
}

func (app *app) loadRun(rev *review.Review, logRun *logRun) {
	if logRun.loaded || logRun.loading {
		return
	}
	logRun.loading = true
	repo, scope, id, provider := rev.Repo, rev.Scope, logRun.info.ID, logRun.info.Agent
	app.async(func() {
		entries, err := app.store.ReadRun(repo, scope, id)
		var items []logItem
		for _, e := range entries {
			items = append(items, logItems(provider, e)...)
		}
		app.update(func() {
			logRun.loaded, logRun.loading = true, false
			if usage := logRun.add(items); logRun.info.Usage == nil && usage != nil {
				logRun.info.Usage = usage
				fillCosts(app.logsOf(rev))
			}
			if err != nil {
				logRun.err = err.Error()
			}
		})
	})
}

// startRun adds a run to the log of a review in memory.
func (app *app) startRun(rev *review.Review, info store.RunInfo) *logRun {
	run := &logRun{info: info, loaded: true, running: true}
	runs := append([]*logRun{run}, app.logsOf(rev)...)
	app.logs[rev] = runs[:min(len(runs), store.MaxRuns)]
	return run
}

// runRecorder records an agent run in the log of its review: in memory,
// where the log panel reads it, and in the run's file.
type runRecorder struct {
	run      *logRun
	provider agent.Provider
	file     runFile
	mu       sync.Mutex
	// items are the log items recorded since the last take.
	items []logItem
}

// entry records a log entry of the run. The agent calls it on its own
// goroutine.
func (runRecorder *runRecorder) entry(entry agent.LogEntry) {
	runRecorder.file.write(entry)
	items := logItems(runRecorder.provider, entry)
	runRecorder.mu.Lock()
	runRecorder.items = append(runRecorder.items, items...)
	runRecorder.mu.Unlock()
}

// take returns the log items recorded since the last take.
func (runRecorder *runRecorder) take() []logItem {
	runRecorder.mu.Lock()
	defer runRecorder.mu.Unlock()
	items := runRecorder.items
	runRecorder.items = nil
	return items
}

// createRunFile creates the file of the run, away from the main thread.
// repo, scope and info are those of rev and the run when it started. A Start Over or an
// archive that came while the file was created leaves it in the folder of
// the review that took the place of rev, so the file is removed again.
func (app *app) createRunFile(rec *runRecorder, rev *review.Review, repo string, scope gitx.Scope, info store.RunInfo) {
	writer, err := app.store.CreateRun(repo, scope, info)
	rec.file.opened(writer)
	app.update(func() {
		if err != nil {
			rec.run.err = err.Error()
		}
		if writer != nil && app.reviews.Obsolete(rev) {
			app.async(func() {
				rec.file.close()
				if err := app.store.RemoveRun(repo, scope, info.ID); err != nil && !store.IsNotExist(err) {
					log.Printf("run log: %v", err)
				}
			})
		}
	})
}

// endRun marks the run as ended and saves what it spent, unless its review
// was replaced.
func (app *app) endRun(rec *runRecorder, rev *review.Review) {
	run := rec.run
	run.running = false
	if usage := run.info.Usage; usage != nil && !app.reviews.Obsolete(rev) {
		usage.CostUSD = runCost(app.logsOf(rev), run)
		if err := app.store.SaveRunUsage(rev.Repo, rev.Scope, run.info.ID, *usage); err != nil {
			log.Printf("run usage: %v", err)
		}
	}
}

// runFile writes the log entries of a run to its file. The file is
// created away from the main thread, so the entries that come before it
// exists wait in memory.
type runFile struct {
	mu      sync.Mutex
	created bool
	writer  *store.RunWriter
	early   []agent.LogEntry
}

// write writes e, or keeps it until the file is created.
func (runFile *runFile) write(entry agent.LogEntry) {
	runFile.mu.Lock()
	defer runFile.mu.Unlock()
	if !runFile.created {
		runFile.early = append(runFile.early, entry)
		return
	}
	runFile.writeLocked(entry)
}

// opened writes the entries that waited into w. A nil w is a file that
// could not be created: the entries then only live in memory.
func (runFile *runFile) opened(file *store.RunWriter) {
	runFile.mu.Lock()
	defer runFile.mu.Unlock()
	runFile.created, runFile.writer = true, file
	for _, e := range runFile.early {
		runFile.writeLocked(e)
	}
	runFile.early = nil
}

func (runFile *runFile) writeLocked(entry agent.LogEntry) {
	if runFile.writer == nil {
		return
	}
	if err := runFile.writer.Write(entry); err != nil {
		log.Printf("run log: %v", err)
		runFile.closeLocked()
	}
}

func (runFile *runFile) close() {
	runFile.mu.Lock()
	defer runFile.mu.Unlock()
	runFile.closeLocked()
}

func (runFile *runFile) closeLocked() {
	if runFile.writer == nil {
		return
	}
	if err := runFile.writer.Close(); err != nil {
		log.Printf("run log: %v", err)
	}
	runFile.writer = nil
}

func (app *app) showLog(target string) {
	app.logOpen, app.logTarget = true, target
}
