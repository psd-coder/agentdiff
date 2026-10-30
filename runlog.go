package main

import (
	"fmt"
	"log"
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

// status is how the run ended, read from its last item.
func (r *logRun) status() string {
	if r.running {
		return "running"
	}
	if !r.loaded || len(r.items) == 0 {
		return ""
	}
	last := r.items[len(r.items)-1]
	switch {
	case last.Title == "Stopped":
		return "stopped"
	case last.Failed:
		return "failed"
	}
	return ""
}

// addUsage counts what the items of a run report as spent.
func (r *logRun) addUsage(items []logItem) {
	for _, it := range items {
		if it.Usage == nil {
			continue
		}
		if r.info.Usage == nil {
			r.info.Usage = &agent.Usage{}
		}
		r.info.Usage.Add(*it.Usage)
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
	b.WriteString(r.label() + "\n")
	if r.info.Note != "" {
		b.WriteString(r.info.Note + "\n")
	}
	for _, it := range r.items {
		fmt.Fprintf(&b, "\n[%s] %s\n", it.time.Format("15:04:05"), it.Title)
		if it.Detail != "" {
			b.WriteString(it.Detail + "\n")
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
	a.async(func() {
		entries, err := a.store.ReadRun(rev.Repo, rev.Scope, r.info.ID)
		var items []logItem
		for _, e := range entries {
			items = append(items, logItems(r.info.Agent, e)...)
		}
		a.update(func() {
			r.items, r.loaded, r.loading = items, true, false
			if r.info.Usage == nil {
				r.addUsage(items)
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
		log.Printf("run log: %v", err)
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
