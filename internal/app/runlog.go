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

func logItems(p agent.Provider, e agent.LogEntry) []logItem {
	var items []logItem
	for _, it := range agent.Summarize(p, e) {
		items = append(items, logItem{time: e.Time, LogItem: it})
	}
	return items
}

// add appends items to the run and reads how it ended from all its
// items. It returns what they report as spent, nil when none does.
func (r *logRun) add(items []logItem) *agent.Usage {
	r.items = append(r.items, items...)
	all := make([]agent.LogItem, len(r.items))
	for i, it := range r.items {
		all[i] = it.LogItem
	}
	r.end = agent.Status(all)
	return agent.SumUsage(all)
}

func (r *logRun) status() string {
	if r.running {
		return "running"
	}
	switch r.end {
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
func runCost(runs []*logRun, r *logRun) float64 {
	var parent *agent.Usage
	if p := resumedRun(runs, r); p != nil {
		parent = p.info.Usage
	}
	return agent.OwnCost(*r.info.Usage, r.info.Resumed != "", parent)
}

// fillCosts works out the own cost of the runs that only know the cost of
// their session. A run read from its log without a usage file is one, and
// so is a run whose resumed run had no usage when it ended.
func fillCosts(runs []*logRun) {
	for _, r := range runs {
		if u := r.info.Usage; u != nil && !r.running && u.CostUSD == 0 && u.SessionCost > 0 {
			u.CostUSD = runCost(runs, r)
		}
	}
}

func (r *logRun) label() string {
	parts := []string{r.info.Title, providerName(r.info.Agent)}
	if r.info.Model != "" {
		parts = append(parts, r.info.Model)
	}
	if r.info.Effort != "" {
		parts = append(parts, r.info.Effort+" effort")
	}
	parts = append(parts, r.info.Started.Format("Jan 2 15:04:05"))
	if u := r.info.Usage; u != nil {
		parts = append(parts, u.Short())
	}
	if s := r.status(); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

// text is the run as plain text, for the clipboard.
func (r *logRun) text() string {
	var b strings.Builder
	b.WriteString(r.label())
	b.WriteString("\n")
	if r.info.Note != "" {
		b.WriteString(r.info.Note)
		b.WriteString("\n")
	}
	for _, it := range r.items {
		fmt.Fprintf(&b, "\n[%s] %s\n", it.time.Format("15:04:05"), it.Title)
		if it.Detail != "" {
			b.WriteString(it.Detail)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// logsOf returns the runs of a review, newest first, reading the list
// from the store the first time.
func (a *app) logsOf(rev *review.Review) []*logRun {
	if runs, ok := a.logs[rev]; ok {
		return runs
	}
	var runs []*logRun
	for _, info := range a.store.Runs(rev.Repo, rev.Scope) {
		runs = append(runs, &logRun{info: info})
	}
	a.logs[rev] = runs
	return runs
}

func (a *app) loadRun(rev *review.Review, r *logRun) {
	if r.loaded || r.loading {
		return
	}
	r.loading = true
	repo, scope, id, provider := rev.Repo, rev.Scope, r.info.ID, r.info.Agent
	a.async(func() {
		entries, err := a.store.ReadRun(repo, scope, id)
		var items []logItem
		for _, e := range entries {
			items = append(items, logItems(provider, e)...)
		}
		a.update(func() {
			r.loaded, r.loading = true, false
			if u := r.add(items); r.info.Usage == nil && u != nil {
				r.info.Usage = u
				fillCosts(a.logsOf(rev))
			}
			if err != nil {
				r.err = err.Error()
			}
		})
	})
}

// startRun adds a run to the log of a review in memory.
func (a *app) startRun(rev *review.Review, info store.RunInfo) *logRun {
	run := &logRun{info: info, loaded: true, running: true}
	runs := append([]*logRun{run}, a.logsOf(rev)...)
	a.logs[rev] = runs[:min(len(runs), store.MaxRuns)]
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
func (r *runRecorder) entry(e agent.LogEntry) {
	r.file.write(e)
	items := logItems(r.provider, e)
	r.mu.Lock()
	r.items = append(r.items, items...)
	r.mu.Unlock()
}

// take returns the log items recorded since the last take.
func (r *runRecorder) take() []logItem {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := r.items
	r.items = nil
	return items
}

// createRunFile creates the file of the run, away from the main thread.
// repo, scope and info are those of rev and the run when it started. A Start Over or an
// archive that came while the file was created leaves it in the folder of
// the review that took the place of rev, so the file is removed again.
func (a *app) createRunFile(rec *runRecorder, rev *review.Review, repo string, scope gitx.Scope, info store.RunInfo) {
	w, err := a.store.CreateRun(repo, scope, info)
	rec.file.opened(w)
	a.update(func() {
		if err != nil {
			rec.run.err = err.Error()
		}
		if w != nil && a.reviews.Obsolete(rev) {
			a.async(func() {
				rec.file.close()
				if err := a.store.RemoveRun(repo, scope, info.ID); err != nil && !store.IsNotExist(err) {
					log.Printf("run log: %v", err)
				}
			})
		}
	})
}

// endRun marks the run as ended and saves what it spent, unless its review
// was replaced.
func (a *app) endRun(rec *runRecorder, rev *review.Review) {
	run := rec.run
	run.running = false
	if u := run.info.Usage; u != nil && !a.reviews.Obsolete(rev) {
		u.CostUSD = runCost(a.logsOf(rev), run)
		if err := a.store.SaveRunUsage(rev.Repo, rev.Scope, run.info.ID, *u); err != nil {
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
	w       *store.RunWriter
	early   []agent.LogEntry
}

// write writes e, or keeps it until the file is created.
func (f *runFile) write(e agent.LogEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.created {
		f.early = append(f.early, e)
		return
	}
	f.writeLocked(e)
}

// opened writes the entries that waited into w. A nil w is a file that
// could not be created: the entries then only live in memory.
func (f *runFile) opened(w *store.RunWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created, f.w = true, w
	for _, e := range f.early {
		f.writeLocked(e)
	}
	f.early = nil
}

func (f *runFile) writeLocked(e agent.LogEntry) {
	if f.w == nil {
		return
	}
	if err := f.w.Write(e); err != nil {
		log.Printf("run log: %v", err)
		f.closeLocked()
	}
}

func (f *runFile) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeLocked()
}

func (f *runFile) closeLocked() {
	if f.w == nil {
		return
	}
	if err := f.w.Close(); err != nil {
		log.Printf("run log: %v", err)
	}
	f.w = nil
}

func (a *app) showLog(target string) {
	a.logOpen, a.logTarget = true, target
}
