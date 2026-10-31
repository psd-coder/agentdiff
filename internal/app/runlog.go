package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
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

// runsOf returns the runs of a review, newest first, reading the list
// from the store the first time.
func (a *app) runsOf(rev *review.Review) []*logRun {
	if runs, ok := a.runs[rev]; ok {
		return runs
	}
	var runs []*logRun
	for _, info := range a.store.Runs(rev.Repo, rev.Scope) {
		runs = append(runs, &logRun{info: info})
	}
	a.runs[rev] = runs
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
				fillCosts(a.runsOf(rev))
			}
			if err != nil {
				r.err = err.Error()
			}
		})
	})
}

// startRun adds a run to the log of a review and opens a file for it.
// The returned writer is nil when the file could not be created; the run
// then only lives in memory.
func (a *app) startRun(rev *review.Review, info store.RunInfo) (*logRun, *store.RunWriter) {
	run := &logRun{info: info, loaded: true, running: true}
	runs := append([]*logRun{run}, a.runsOf(rev)...)
	a.runs[rev] = runs[:min(len(runs), store.MaxRuns)]
	w, err := a.store.CreateRun(rev.Repo, rev.Scope, info)
	if err != nil {
		run.err = err.Error()
		return run, nil
	}
	return run, w
}

func (a *app) showLog(target string) {
	a.logOpen, a.logTarget = true, target
}

func newRunInfo(target, title, note string, cfg agent.Config) store.RunInfo {
	now := time.Now()
	return store.RunInfo{
		ID: store.NewRunID(now), Target: target, Title: title, Note: note,
		Agent: cfg.Provider, Model: cfg.Model, Effort: cfg.Effort, Started: now,
	}
}
