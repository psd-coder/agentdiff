package app

import (
	"testing"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/store"
)

// TestRunFileKeepsEarlyEntries writes log entries before the run file
// exists, while it is open and after it is closed: the file gets the
// first two, in order.
func TestRunFileKeepsEarlyEntries(t *testing.T) {
	s := store.Store{Dir: t.TempDir()}
	scope := gitx.Scope{Kind: gitx.ScopeUncommitted}
	entry := func(text string) agent.LogEntry {
		return agent.LogEntry{Time: time.Now(), Kind: agent.LogStdout, Text: text}
	}
	f := &runFile{}
	f.write(entry("before the file"))
	w, err := s.CreateRun("/repo", scope, store.RunInfo{ID: "run1"})
	if err != nil {
		t.Fatal(err)
	}
	f.opened(w)
	f.write(entry("while open"))
	f.close()
	f.write(entry("after close"))

	entries, err := s.ReadRun("/repo", scope, "run1")
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
