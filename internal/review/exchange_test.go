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

func checkPrompt(t *testing.T, prompt string, want, lacks []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(prompt, w) {
			t.Errorf("prompt lacks %q", w)
		}
	}
	for _, l := range lacks {
		if strings.Contains(prompt, l) {
			t.Errorf("prompt has %q", l)
		}
	}
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
			checkPrompt(t, req.Prompt, tt.want, tt.lacks)
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

// walkReview has an overview with a claude session and two steps, a walk
// into its first step with its own claude session, and a question about
// that walk's step.
func walkReview(scope gitx.Scope) (r *Review, inner, question *Walk) {
	r = New("/repo", scope)
	r.Overview = &Overview{
		Walk:     &Walkthrough{Steps: []Step{{Title: "Cache", Text: "c"}, {Title: "Keys", Text: "k"}}},
		Sessions: Sessions{agent.Claude: "root"},
	}
	inner = r.AddWalk("", 0, "")
	inner.Content = &Walkthrough{Steps: []Step{{Title: "Lower-case keys", Text: "Get lower-cases.", Refs: []Ref{{File: "c.go", Side: SideNew, Start: 6, End: 6, Why: "the change"}}}}}
	inner.Sessions = Sessions{agent.Claude: "inner"}
	question = r.AddWalk(inner.ID, -1, "Is it tested?")
	return r, inner, question
}

func TestDeeperExchangeRequest(t *testing.T) {
	tests := []struct {
		name     string
		provider agent.Provider
		scope    gitx.Scope
		walk     func(r *Review, inner, question *Walk) *Walk
		resume   string
		want     []string
		lacks    []string
	}{
		{
			name:     "a step of the overview forks the overview session",
			provider: agent.Claude,
			walk:     func(r *Review, _, _ *Walk) *Walk { return r.AddWalk("", 1, "") },
			resume:   "root",
			want:     []string{"### Keys\nk\n", "more detail", walkSchema},
			lacks:    []string{"Diffstat", "went deeper"},
		},
		{
			name:     "a step of the overview without a session of the provider starts with the change set",
			provider: agent.Codex,
			scope:    gitx.Scope{Kind: gitx.ScopeUncommitted},
			walk:     func(r *Review, _, _ *Walk) *Walk { return r.AddWalk("", 1, "") },
			want: []string{
				"the uncommitted changes in the working tree",
				"Diffstat:\n```\na.go | 1 +\n b.go | 2 +-\n```",
				"The reviewer reads a walkthrough of these changes.\n",
				"### Keys",
			},
		},
		{
			name:     "a step of a walk forks the walk session",
			provider: agent.Claude,
			walk:     func(r *Review, inner, _ *Walk) *Walk { return r.AddWalk(inner.ID, 0, "") },
			resume:   "inner",
			want:     []string{"### Lower-case keys", "c.go:6 (+): the change"},
			lacks:    []string{"Diffstat"},
		},
		{
			name:     "a question about a walk without a session of the provider names the path",
			provider: agent.Codex,
			scope:    gitx.Scope{Kind: gitx.ScopeCommit, Commit: "abc"},
			walk:     func(_ *Review, _, question *Walk) *Walk { return question },
			want: []string{
				"commit abc",
				"went deeper into: Cache.\n",
				"The reviewer asks: Is it tested?",
				"git show abc:<path> | cat -n",
			},
			lacks: []string{"###", "more detail"},
		},
		{
			name:     "a question two levels down names the whole path",
			provider: agent.Codex,
			walk:     func(r *Review, _, question *Walk) *Walk { return r.AddWalk(question.ID, -1, "And then?") },
			want:     []string{"went deeper into: Cache > Is it tested?.\n", "The reviewer asks: And then?"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, inner, question := walkReview(tt.scope)
			req := DeeperExchange(r, tt.walk(r, inner, question), agent.Config{Provider: tt.provider}, testChanges.Diffstat).Request
			if req.Dir != "/repo" || req.SystemPrompt != SystemPrompt {
				t.Errorf("dir %q, system prompt %q", req.Dir, req.SystemPrompt)
			}
			if req.ResumeID != tt.resume || req.Fork != (tt.resume != "") {
				t.Errorf("resumes %q (fork %v), want %q", req.ResumeID, req.Fork, tt.resume)
			}
			checkPrompt(t, req.Prompt, tt.want, tt.lacks)
		})
	}
}

func TestDeeperExchangeClearsTheWalkAndItsSessions(t *testing.T) {
	r, inner, _ := walkReview(gitx.Scope{Kind: gitx.ScopeUncommitted})
	inner.Sessions.Set(agent.Codex, "x0")
	inner.Markdown, inner.Err, inner.Agent, inner.Model = "old", "boom", agent.Claude, "opus"
	inner.More = []Turn{{Question: "And then?"}}
	DeeperExchange(r, inner, agent.Config{Provider: agent.Claude}, "")
	want := Walk{ID: inner.ID, Step: 0, Generated: inner.Generated}
	if !reflect.DeepEqual(*inner, want) {
		t.Errorf("walk = %+v\nwant   %+v", *inner, want)
	}
}

func TestFollowupExchangeRequest(t *testing.T) {
	tests := []struct {
		name     string
		provider agent.Provider
		resume   string
		want     []string
		lacks    []string
	}{
		{
			name:     "a walk with a session of the provider goes on in it",
			provider: agent.Claude,
			resume:   "q1",
			want:     []string{"follow-up question: And then?", walkSchema},
			lacks:    []string{"Diffstat", "Q: Why?"},
		},
		{
			name:     "a walk without a session of the provider starts with the change set and the earlier turns",
			provider: agent.Codex,
			want: []string{
				"Diffstat:\n```\na.go | 1 +\n b.go | 2 +-\n```",
				"Q: Why?\nA: Because.\n\nQ: How?\n\n",
				"follow-up question: And then?",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted})
			w := r.AddWalk("", -1, "Why?")
			w.Content = &Walkthrough{Summary: "Because."}
			w.Sessions = Sessions{agent.Claude: "q1"}
			w.More = []Turn{{Question: "How?", Err: "boom"}}
			req := FollowupExchange(r, w, agent.Config{Provider: tt.provider}, testChanges.Diffstat, "And then?").Request
			if req.ResumeID != tt.resume || req.Fork {
				t.Errorf("resumes %q (fork %v), want %q without a fork", req.ResumeID, req.Fork, tt.resume)
			}
			checkPrompt(t, req.Prompt, tt.want, tt.lacks)
			if n := len(w.More); n != 2 || w.More[1].Question != "And then?" || w.More[1].Content != nil {
				t.Errorf("turns after the follow-up = %+v", w.More)
			}
		})
	}
}

func TestWalkExchangeResults(t *testing.T) {
	at := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	json := "```json\n" + `{"summary": "s", "steps": [{"title": "t", "text": "x"}]}` + "\n```"
	cfg := agent.Config{Provider: agent.Codex, Model: "gpt"}
	first := func(r *Review) (*Walk, *Exchange) {
		w := r.AddWalk("", -1, "Why?")
		return w, DeeperExchange(r, w, cfg, "")
	}
	followup := func(r *Review) (*Walk, *Exchange) {
		w := r.AddWalk("", -1, "Why?")
		w.Content, w.Agent, w.Model = &Walkthrough{Summary: "Because."}, agent.Claude, "opus"
		w.Sessions = Sessions{agent.Claude: "c0"}
		return w, FollowupExchange(r, w, cfg, "", "And then?")
	}
	tests := []struct {
		name  string
		start func(r *Review) (*Walk, *Exchange)
		apply func(e *Exchange)
		check func(t *testing.T, w *Walk)
	}{
		{
			name:  "a walkthrough answer becomes the content, by the run's agent",
			start: first,
			apply: func(e *Exchange) { e.Started("x1"); e.Answered(json, at) },
			check: func(t *testing.T, w *Walk) {
				want := &Walkthrough{Summary: "s", Steps: []Step{{Title: "t", Text: "x"}}}
				if !reflect.DeepEqual(w.Content, want) || w.Markdown != "" || !w.Generated.Equal(at) {
					t.Errorf("content %+v, markdown %q, generated %v", w.Content, w.Markdown, w.Generated)
				}
				if w.Agent != agent.Codex || w.Model != "gpt" || w.Sessions[agent.Codex] != "x1" {
					t.Errorf("agent %q, model %q, sessions %v", w.Agent, w.Model, w.Sessions)
				}
			},
		},
		{
			name:  "an answer without a walkthrough is kept as Markdown",
			start: first,
			apply: func(e *Exchange) { e.Answered("## Plain\nText.", at) },
			check: func(t *testing.T, w *Walk) {
				if w.Content != nil || w.Markdown != "## Plain\nText." || w.Agent != agent.Codex {
					t.Errorf("content %+v, markdown %q, agent %q", w.Content, w.Markdown, w.Agent)
				}
			},
		},
		{
			name:  "a follow-up answer goes into the latest turn and keeps the first agent",
			start: followup,
			apply: func(e *Exchange) { e.Started("x1"); e.Answered("Plain.", at) },
			check: func(t *testing.T, w *Walk) {
				if w.Content.Summary != "Because." || w.More[0].Markdown != "Plain." || !w.More[0].Generated.Equal(at) {
					t.Errorf("content %+v, turns %+v", w.Content, w.More)
				}
				if w.Agent != agent.Claude || w.Model != "opus" {
					t.Errorf("agent %q, model %q, want the first answer's", w.Agent, w.Model)
				}
				if want := (Sessions{agent.Claude: "c0", agent.Codex: "x1"}); !reflect.DeepEqual(w.Sessions, want) {
					t.Errorf("sessions %v, want %v", w.Sessions, want)
				}
			},
		},
		{
			name:  "a failure after the start keeps the session and shows the error",
			start: first,
			apply: func(e *Exchange) { e.Started("x1"); e.Failed("boom", at) },
			check: func(t *testing.T, w *Walk) {
				if w.Err != "boom" || w.Content != nil || w.Agent != "" || w.Sessions[agent.Codex] != "x1" {
					t.Errorf("err %q, content %+v, agent %q, sessions %v", w.Err, w.Content, w.Agent, w.Sessions)
				}
			},
		},
		{
			name:  "a stopped follow-up shows Stopped in the latest turn",
			start: followup,
			apply: func(e *Exchange) { e.Stopped(at) },
			check: func(t *testing.T, w *Walk) {
				if w.Err != "" || w.More[0].Err != "Stopped" || w.Content.Summary != "Because." {
					t.Errorf("err %q, turns %+v", w.Err, w.More)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, e := tt.start(New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted}))
			tt.apply(e)
			tt.check(t, w)
		})
	}
}
