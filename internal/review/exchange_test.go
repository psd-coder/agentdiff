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
		name     string
		provider agent.Provider
		scope    gitx.Scope
		prompts  Prompts
		want     []string
		lacks    []string
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
				"Add a section \"Checklist\"",
				"\"groups\"", "\"sections\"", "\"refs\"",
				"The changed files are:\n- a.go\n- b.go\n",
			},
			lacks: []string{"git show"},
		},
		{
			name:     "codex on a commit with its own questions",
			provider: agent.Codex,
			scope:    gitx.Scope{Kind: gitx.ScopeCommit, Commit: "abc123"},
			prompts:  Prompts{PromptOverview: DefaultOverview("Is it fast?")},
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
			req := OverviewExchange(r, testChanges, agent.Config{Provider: tt.provider}, tt.prompts).Request
			if req.Dir != "/repo" || req.SystemPrompt != systemPrompt {
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
			e := OverviewExchange(r, testChanges, agent.Config{Provider: agent.Codex, Model: "gpt"}, nil)
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
			req := DeeperExchange(r, tt.walk(r, inner, question), agent.Config{Provider: tt.provider}, nil, testChanges.Diffstat).Request
			if req.Dir != "/repo" || req.SystemPrompt != systemPrompt {
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
	DeeperExchange(r, inner, agent.Config{Provider: agent.Claude}, nil, "")
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
			req := FollowupExchange(r, w, agent.Config{Provider: tt.provider}, nil, testChanges.Diffstat, "And then?").Request
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
		return w, DeeperExchange(r, w, cfg, nil, "")
	}
	followup := func(r *Review) (*Walk, *Exchange) {
		w := r.AddWalk("", -1, "Why?")
		w.Content, w.Agent, w.Model = &Walkthrough{Summary: "Because."}, agent.Claude, "opus"
		w.Sessions = Sessions{agent.Claude: "c0"}
		return w, FollowupExchange(r, w, cfg, nil, "", "And then?")
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

func TestThreadExchangeRequest(t *testing.T) {
	lines := Anchor{Kind: AnchorLines, File: "a.go", Side: SideNew, Start: 3, Quote: "x := 1"}
	earlier := []Message{{Role: RoleUser, Text: "Why?"}, {Role: RoleAgent, Text: "Because."}}
	tests := []struct {
		name     string
		provider agent.Provider
		overview Sessions
		thread   Sessions
		messages []Message
		resume   string
		fork     bool
		want     []string
		lacks    []string
	}{
		{
			name:     "the first question forks the overview session of the provider",
			provider: agent.Claude,
			overview: Sessions{agent.Claude: "c0", agent.Codex: "x0"},
			resume:   "c0",
			fork:     true,
			want:     []string{"new, separate discussion thread", "a.go:3 (+)", "x := 1", "The reviewer asks:\n\nAnd then?"},
			lacks:    []string{"Diffstat", "so far"},
		},
		{
			name:     "the first question without an overview session of the provider starts with the change set",
			provider: agent.Codex,
			overview: Sessions{agent.Claude: "c0"},
			want:     []string{"commit abc123", "Diffstat:\n```\na.go | 1 +\n b.go | 2 +-\n```", "a.go:3 (+)", "And then?"},
		},
		{
			name:     "a later question goes on in the thread session",
			provider: agent.Claude,
			overview: Sessions{agent.Claude: "c0"},
			thread:   Sessions{agent.Claude: "t1"},
			messages: earlier,
			resume:   "t1",
			want:     []string{"And then?"},
			lacks:    []string{"a.go", "Because."},
		},
		{
			name:     "a later question to a new session of the provider forks the overview with the thread so far",
			provider: agent.Codex,
			overview: Sessions{agent.Codex: "x0"},
			thread:   Sessions{agent.Claude: "t1"},
			messages: earlier,
			resume:   "x0",
			fork:     true,
			want:     []string{"a.go:3 (+)", "Reviewer: Why?\n\nAgent: Because.\n\n", "The reviewer asks:\n\nAnd then?"},
			lacks:    []string{"Reviewer: And then?"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New("/repo", gitx.Scope{Kind: gitx.ScopeCommit, Commit: "abc123"})
			r.Overview = &Overview{Sessions: tt.overview}
			th := r.AddThread(lines)
			th.Sessions = tt.thread
			th.Messages = append([]Message(nil), tt.messages...)
			req := ThreadExchange(r, th, agent.Config{Provider: tt.provider}, nil, testChanges.Diffstat, "And then?").Request
			if req.Dir != "/repo" || req.SystemPrompt != systemPrompt {
				t.Errorf("dir %q, system prompt %q", req.Dir, req.SystemPrompt)
			}
			if req.ResumeID != tt.resume || req.Fork != tt.fork {
				t.Errorf("resumes %q (fork %v), want %q (fork %v)", req.ResumeID, req.Fork, tt.resume, tt.fork)
			}
			checkPrompt(t, req.Prompt, tt.want, tt.lacks)
			last := th.Messages[len(th.Messages)-1]
			if len(th.Messages) != len(tt.messages)+1 || last.Role != RoleUser || last.Text != "And then?" {
				t.Errorf("messages after the question = %+v", th.Messages)
			}
			if th.Agent != tt.provider {
				t.Errorf("thread agent %q, want %q", th.Agent, tt.provider)
			}
		})
	}
}

func TestThreadExchangeWithoutOverview(t *testing.T) {
	r := New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted})
	th := r.AddThread(Anchor{Kind: AnchorGeneral})
	req := ThreadExchange(r, th, agent.Config{Provider: agent.Claude}, nil, "", "Why?").Request
	if req.ResumeID != "" || req.Fork {
		t.Errorf("resumes %q (fork %v), want a new session", req.ResumeID, req.Fork)
	}
	checkPrompt(t, req.Prompt, []string{"uncommitted changes", "the change set as a whole", "Why?"}, nil)
}

func TestThreadExchangeResults(t *testing.T) {
	at := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		apply    func(e *Exchange, th *Thread)
		want     []Message
		sessions Sessions
	}{
		{
			name:     "the answer follows the question, and the session is kept",
			apply:    func(e *Exchange, _ *Thread) { e.Started("x1"); e.Answered("Because.", at) },
			want:     []Message{{Role: RoleUser, Text: "Why?"}, {Role: RoleAgent, Text: "Because.", Time: at}},
			sessions: Sessions{agent.Claude: "c1", agent.Codex: "x1"},
		},
		{
			name: "a comment during the run comes after the answer",
			apply: func(e *Exchange, th *Thread) {
				th.Messages = append(th.Messages, Message{Role: RoleUser, Text: "Keep it short."})
				e.Answered("Because.", at)
			},
			want: []Message{
				{Role: RoleUser, Text: "Why?"},
				{Role: RoleAgent, Text: "Because.", Time: at},
				{Role: RoleUser, Text: "Keep it short."},
			},
			sessions: Sessions{agent.Claude: "c1"},
		},
		{
			name:     "a failure after the start keeps the session and shows the error",
			apply:    func(e *Exchange, _ *Thread) { e.Started("x1"); e.Failed("boom", at) },
			want:     []Message{{Role: RoleUser, Text: "Why?"}, {Role: RoleAgent, Err: "boom", Time: at}},
			sessions: Sessions{agent.Claude: "c1", agent.Codex: "x1"},
		},
		{
			name:     "a stopped run shows Stopped",
			apply:    func(e *Exchange, _ *Thread) { e.Stopped(at) },
			want:     []Message{{Role: RoleUser, Text: "Why?"}, {Role: RoleAgent, Err: "Stopped", Time: at}},
			sessions: Sessions{agent.Claude: "c1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted})
			th := r.AddThread(Anchor{Kind: AnchorGeneral})
			th.Sessions = Sessions{agent.Claude: "c1"}
			e := ThreadExchange(r, th, agent.Config{Provider: agent.Codex}, nil, "", "Why?")
			th.Messages[0].Time = time.Time{}
			tt.apply(e, th)
			if !reflect.DeepEqual(th.Messages, tt.want) {
				t.Errorf("messages = %+v\nwant       %+v", th.Messages, tt.want)
			}
			if !reflect.DeepEqual(th.Sessions, tt.sessions) {
				t.Errorf("sessions = %v, want %v", th.Sessions, tt.sessions)
			}
			if r.Overview != nil || len(r.Walks) != 0 {
				t.Errorf("the thread run wrote the overview %+v or walks %+v", r.Overview, r.Walks)
			}
		})
	}
}

func TestExchangeTargetAndKind(t *testing.T) {
	r := New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted})
	w := r.AddWalk("", -1, "Why?")
	th := r.AddThread(Anchor{Kind: AnchorGeneral})
	cfg := agent.Config{Provider: agent.Claude}
	tests := []struct {
		name   string
		ex     *Exchange
		target string
		kind   Kind
	}{
		{"overview", OverviewExchange(r, testChanges, cfg, nil), "overview", KindOverview},
		{"deeper", DeeperExchange(r, w, cfg, nil, ""), w.ID, KindWalk},
		{"follow-up", FollowupExchange(r, w, cfg, nil, "", "And?"), w.ID, KindWalk},
		{"thread", ThreadExchange(r, th, cfg, nil, "", "Why?"), th.ID, KindThread},
	}
	for _, tc := range tests {
		if got := tc.ex.Target(); got != tc.target {
			t.Errorf("%s: target %q, want %q", tc.name, got, tc.target)
		}
		if got := tc.ex.Kind(); got != tc.kind {
			t.Errorf("%s: kind %v, want %v", tc.name, got, tc.kind)
		}
	}
}
