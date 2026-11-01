package app

import (
	"testing"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

// TestRunFileKeepsEarlyEntries writes log entries before the run file
// exists, while it is open and after it is closed: the file gets the
// first two, in order.
func TestRunFileKeepsEarlyEntries(t *testing.T) {
	storage := store.Store{Dir: t.TempDir()}
	scope := gitx.Scope{Kind: gitx.ScopeUncommitted}
	entry := func(text string) agent.LogEntry {
		return agent.LogEntry{Time: time.Now(), Kind: agent.LogStdout, Text: text}
	}
	file := &runFile{}
	file.write(entry("before the file"))
	writer, err := storage.CreateRun("/repo", scope, store.RunInfo{ID: "run1"})
	if err != nil {
		t.Fatal(err)
	}
	file.opened(writer)
	file.write(entry("while open"))
	file.close()
	file.write(entry("after close"))

	entries, err := storage.ReadRun("/repo", scope, "run1")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Text)
	}
	if len(got) != 2 || got[0] != "before the file" || got[1] != "while open" {
		t.Errorf("entries %q", got)
	}
}

// TestRunFileOfReplacedReview creates the file of a run after its review
// was started over: the file goes, so the new review shows no run of the
// old one. The file of a run of the new review stays.
func TestRunFileOfReplacedReview(t *testing.T) {
	app := newApp(store.Store{Dir: t.TempDir()})
	scope := gitx.Scope{Kind: gitx.ScopeUncommitted}
	create := func(rev *review.Review, id string) {
		info := store.RunInfo{ID: id}
		rec := &runRecorder{run: app.startRun(rev, info)}
		app.createRunFile(rec, rev, "/repo", scope, info)
	}

	old := app.reviews.Of("/repo", scope)
	fresh, err := app.reviews.StartOver("/repo", scope)
	if err != nil {
		t.Fatal(err)
	}
	create(old, "replaced")
	create(fresh, "kept")

	var ids []string
	for _, r := range app.store.Runs("/repo", scope) {
		ids = append(ids, r.ID)
	}
	if len(ids) != 1 || ids[0] != "kept" {
		t.Errorf("run files %q", ids)
	}
}
