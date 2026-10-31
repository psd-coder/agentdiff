package review

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
)

var testChanges = ChangeSet{
	Diffstat: " a.go | 1 +\n b.go | 2 +-",
	Files:    []string{"a.go", "b.go"},
	PatchID:  "p1",
	FileIDs:  map[string]string{"a.go": "f1", "b.go": "f2"},
}

func TestOverviewExchangeRequest(t *testing.T) {
	tests := []struct {
		name      string
		provider  agent.Provider
		scope     gitx.Scope
		questions string
		want      []string
		lacks     []string
	}{
		{
			name:     "claude on the working tree with the default questions",
			provider: agent.Claude,
			scope:    gitx.Scope{Kind: gitx.ScopeUncommitted},
			want: []string{
				"the uncommitted changes in the working tree",
				"Diffstat:\n```\na.go | 1 +\n b.go | 2 +-\n```",
				"read of the file in the working tree",
				"What data changes",
				"\"groups\"", "\"checklist\"", "\"refs\"",
				"The changed files are:\n- a.go\n- b.go\n",
			},
			lacks: []string{"git show"},
		},
		{
			name:      "codex on a commit with its own questions",
			provider:  agent.Codex,
			scope:     gitx.Scope{Kind: gitx.ScopeCommit, Commit: "abc123"},
			questions: "Is it fast?",
			want: []string{
				"commit abc123",
				"git show abc123:<path>",
				"Is it fast?",
			},
			lacks: []string{"What data changes", "in the working tree"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New("/repo", tt.scope)
			r.Overview = &Overview{Walk: &Walkthrough{Summary: "old"}, Sessions: Sessions{agent.Claude: "c0", agent.Codex: "x0"}}
			req := OverviewExchange(r, testChanges, agent.Config{Provider: tt.provider}, tt.questions).Request
			if req.Dir != "/repo" || req.SystemPrompt != SystemPrompt {
				t.Errorf("dir %q, system prompt %q", req.Dir, req.SystemPrompt)
			}
			if req.ResumeID != "" || req.Fork {
				t.Errorf("the overview resumes %q (fork %v), want a new session", req.ResumeID, req.Fork)
			}
			for _, want := range tt.want {
				if !strings.Contains(req.Prompt, want) {
					t.Errorf("prompt lacks %q", want)
				}
			}
			for _, lack := range tt.lacks {
				if strings.Contains(req.Prompt, lack) {
					t.Errorf("prompt has %q", lack)
				}
			}
		})
	}
}

func TestOverviewExchangeResults(t *testing.T) {
	written := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	at := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	done := func() *Overview {
		return &Overview{
			Walk:      &Walkthrough{Summary: "old"},
			Groups:    []Group{{Title: "Old", Files: []string{"a.go"}}},
			Sessions:  Sessions{agent.Claude: "c0", agent.Codex: "x0"},
			Agent:     agent.Claude,
			Model:     "opus",
			Generated: written,
			PatchID:   "p0",
			Files:     map[string]string{"a.go": "f0"},
		}
	}
	withErr := func(ov *Overview, msg string) Overview {
		ov.Err = msg
		return *ov
	}
	tests := []struct {
		name    string
		old     *Overview
		session string
		apply   func(e *Exchange)
		want    Overview
	}{
		{
			name:    "a JSON answer replaces the overview and keeps the other provider's session",
			old:     done(),
			session: "x1",
			apply: func(e *Exchange) {
				e.Answered("```json\n"+`{"summary": "s", "steps": [{"title": "t", "text": "x"}], "groups": [{"title": "A", "files": ["a.go"]}]}`+"\n```", at)
			},
			want: Overview{
				Walk:      &Walkthrough{Summary: "s", Steps: []Step{{Title: "t", Text: "x"}}},
				Groups:    []Group{{Title: "A", Files: []string{"a.go"}}, {Title: "Other changes", Files: []string{"b.go"}}},
				Sessions:  Sessions{agent.Claude: "c0", agent.Codex: "x1"},
				Agent:     agent.Codex,
				Model:     "gpt",
				Generated: at,
				PatchID:   "p1",
				Files:     map[string]string{"a.go": "f1", "b.go": "f2"},
			},
		},
		{
			name:    "a Markdown answer becomes the overview with its groups block",
			session: "x1",
			apply: func(e *Exchange) {
				e.Answered("## High level\nText.\n\n```json\n"+`{"groups": [{"title": "Keys", "files": ["b.go", "a.go"]}]}`+"\n```", at)
			},
			want: Overview{
				Markdown:  "## High level\nText.",
				Groups:    []Group{{Title: "Keys", Files: []string{"b.go", "a.go"}}},
				Sessions:  Sessions{agent.Codex: "x1"},
				Agent:     agent.Codex,
				Model:     "gpt",
				Generated: at,
				PatchID:   "p1",
				Files:     map[string]string{"a.go": "f1", "b.go": "f2"},
			},
		},
		{
			name:  "an answer without a session keeps the sessions of the old overview",
			old:   done(),
			apply: func(e *Exchange) { e.Answered("Plain text.", at) },
			want: Overview{
				Markdown:  "Plain text.",
				Groups:    []Group{{Title: "Repository root", Files: []string{"a.go", "b.go"}}},
				Sessions:  Sessions{agent.Claude: "c0", agent.Codex: "x0"},
				Agent:     agent.Codex,
				Model:     "gpt",
				Generated: at,
				PatchID:   "p1",
				Files:     map[string]string{"a.go": "f1", "b.go": "f2"},
			},
		},
		{
			name:    "a failure keeps the old overview, its sessions and its agent",
			old:     done(),
			session: "x1",
			apply:   func(e *Exchange) { e.Failed("boom", at) },
			want:    withErr(done(), "boom"),
		},
		{
			name:  "a failure without an overview shows only the error",
			apply: func(e *Exchange) { e.Failed("boom", at) },
			want:  Overview{Generated: at, Err: "boom"},
		},
		{
			name:  "a failure after a failure shows only the new error",
			old:   &Overview{Generated: written, Err: "first"},
			apply: func(e *Exchange) { e.Failed("boom", at) },
			want:  Overview{Generated: at, Err: "boom"},
		},
		{
			name:  "a stopped run shows Stopped over the old overview",
			old:   done(),
			apply: func(e *Exchange) { e.Stopped(at) },
			want:  withErr(done(), "Stopped"),
		},
		{
			name:  "a stopped run without an overview shows only Stopped",
			apply: func(e *Exchange) { e.Stopped(at) },
			want:  Overview{Generated: at, Err: "Stopped"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted})
			r.Overview = tt.old
			e := OverviewExchange(r, testChanges, agent.Config{Provider: agent.Codex, Model: "gpt"}, "")
			if tt.session != "" {
				e.Started(tt.session)
			}
			tt.apply(e)
			if !reflect.DeepEqual(*r.Overview, tt.want) {
				t.Errorf("overview = %+v\nwant       %+v", *r.Overview, tt.want)
			}
		})
	}
}
