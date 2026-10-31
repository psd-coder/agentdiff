package review

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
)

// TestLoadsSessionOfOldReview loads a review file saved while each item
// kept one session. The session goes under the item's agent, and the
// file saved again keeps only the sessions per provider.
func TestLoadsSessionOfOldReview(t *testing.T) {
	old := `{
		"overview": {"agent": "claude", "sessionId": "ov", "markdown": "x"},
		"walks": [{"id": "w1", "agent": "codex", "sessionId": "walk"}],
		"threads": [
			{"id": "t1", "agent": "claude", "sessionId": "thread"},
			{"id": "t2", "sessionId": "orphan"}
		]
	}`
	var r Review
	if err := json.Unmarshal([]byte(old), &r); err != nil {
		t.Fatal(err)
	}
	got := []Sessions{r.Overview.Sessions, r.Walks[0].Sessions, r.Threads[0].Sessions, r.Threads[1].Sessions}
	want := []Sessions{{agent.Claude: "ov"}, {agent.Codex: "walk"}, {agent.Claude: "thread"}, nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sessions = %v, want %v", got, want)
	}
	if r.Overview.Markdown != "x" || r.Walks[0].ID != "w1" {
		t.Errorf("review = %+v", r)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "sessionId") || !strings.Contains(string(b), `"sessions":{"claude":"ov"}`) {
		t.Errorf("saved again = %s", b)
	}
}

func TestParseOverview(t *testing.T) {
	files := []string{"a/x.go", "a/y.go", "b/z.go", "README.md"}
	tests := []struct {
		name       string
		md         string
		wantBody   string
		wantGroups []Group
	}{
		{
			name:     "groups block taken out",
			md:       "## 1. High level\nDid things.\n\n```json\n{\"groups\":[{\"title\":\"Core\",\"summary\":\"S\",\"files\":[\"a/x.go\",\"./b/z.go\"]},{\"title\":\"Docs\",\"files\":[\"README.md\",\"a/x.go\"]}]}\n```\n",
			wantBody: "## 1. High level\nDid things.",
			wantGroups: []Group{
				{Title: "Core", Summary: "S", Files: []string{"a/x.go", "b/z.go"}},
				{Title: "Docs", Files: []string{"README.md"}},
				{Title: "Other changes", Files: []string{"a/y.go"}},
			},
		},
		{
			name:     "no block falls back to directories",
			md:       "## Overview\nText",
			wantBody: "## Overview\nText",
			wantGroups: []Group{
				{Title: "Repository root", Files: []string{"README.md"}},
				{Title: "a", Files: []string{"a/x.go", "a/y.go"}},
				{Title: "b", Files: []string{"b/z.go"}},
			},
		},
		{
			name:     "malformed json keeps the text",
			md:       "Text\n```json\n{\"groups\": [oops]}\n```",
			wantBody: "Text\n```json\n{\"groups\": [oops]}\n```",
			wantGroups: []Group{
				{Title: "Repository root", Files: []string{"README.md"}},
				{Title: "a", Files: []string{"a/x.go", "a/y.go"}},
				{Title: "b", Files: []string{"b/z.go"}},
			},
		},
		{
			name:     "unknown files and empty groups dropped",
			md:       "```json\n{\"groups\":[{\"title\":\"Ghost\",\"files\":[\"nope.go\"]},{\"title\":\"All\",\"files\":[\"a/x.go\",\"a/y.go\",\"b/z.go\",\"README.md\"]}]}\n```",
			wantBody: "",
			wantGroups: []Group{
				{Title: "All", Files: []string{"a/x.go", "a/y.go", "b/z.go", "README.md"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, groups := ParseOverview(tt.md, files)
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
			if !reflect.DeepEqual(groups, tt.wantGroups) {
				t.Errorf("groups = %+v, want %+v", groups, tt.wantGroups)
			}
		})
	}
}

func TestParseOverviewNoFiles(t *testing.T) {
	_, groups := ParseOverview("text", nil)
	if len(groups) != 0 {
		t.Errorf("groups = %+v, want none", groups)
	}
}

func TestSections(t *testing.T) {
	md := "Intro line\n\n## 1. High level\n- a\n\n```go\n## not a heading\n```\n## 2. Data\nNone.\n"
	got := Sections(md)
	want := []Section{
		{Title: "", Body: "Intro line"},
		{Title: "1. High level", Body: "- a\n\n```go\n## not a heading\n```"},
		{Title: "2. Data", Body: "None."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sections = %#v\nwant %#v", got, want)
	}
	if len(Sections("")) != 0 {
		t.Error("empty overview has sections")
	}
}

func TestSectionsEdges(t *testing.T) {
	tests := []struct {
		name string
		md   string
		want []Section
	}{
		{"keeps text without headings as one section", "Just text\nmore\n", []Section{{Body: "Just text\nmore"}}},
		{"keeps a heading on the last line without a newline", "Intro\n## Last", []Section{{Body: "Intro"}, {Title: "Last"}}},
		{"keeps the body after the last heading without a newline", "## A\nx\ny", []Section{{Title: "A", Body: "x\ny"}}},
		{"trims CRLF around titles and bodies and keeps it inside", "Intro\r\n## A\r\nx\r\ny\r\n", []Section{{Body: "Intro"}, {Title: "A", Body: "x\r\ny"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sections(tt.md); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Sections(%q) = %#v\nwant %#v", tt.md, got, tt.want)
			}
		})
	}
}

func TestAnchorLabel(t *testing.T) {
	tests := []struct {
		a    Anchor
		want string
	}{
		{Anchor{Kind: AnchorLines, File: "h.go", Side: SideNew, Start: 43, End: 43}, "h.go:43 (+)"},
		{Anchor{Kind: AnchorLines, File: "h.go", Side: SideNew, Start: 43, End: 67}, "h.go:43-67 (+)"},
		{Anchor{Kind: AnchorLines, File: "s.go", Side: SideOld, Start: 18}, "s.go:18 (-)"},
		{Anchor{Kind: AnchorFile, File: "h.go"}, "h.go (file-level)"},
		{Anchor{Kind: AnchorSection, Section: "2. Data"}, "Overview: 2. Data"},
		{Anchor{}, "General"},
	}
	for _, tt := range tests {
		if got := tt.a.Label(); got != tt.want {
			t.Errorf("Label() = %q, want %q", got, tt.want)
		}
	}
}

func TestExport(t *testing.T) {
	r := New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted})
	line := r.AddThread(Anchor{Kind: AnchorLines, File: "h.go", Side: SideNew, Start: 43, End: 44, Quote: "if err == x {\n}\n"})
	line.Messages = []Message{{Role: RoleUser, Text: "use errors.Is"}}
	disc := r.AddThread(Anchor{Kind: AnchorFile, File: "s.go"})
	disc.Messages = []Message{
		{Role: RoleUser, Text: "why remove validation?"},
		{Role: RoleAgent, Text: "It moved to the caller."},
		{Role: RoleUser, Text: "keep it here too"},
		{Role: RoleAgent, Pending: true},
	}
	resolved := r.AddThread(Anchor{Kind: AnchorGeneral})
	resolved.Messages = []Message{{Role: RoleUser, Text: "done"}}
	resolved.Resolved = true
	excluded := r.AddThread(Anchor{Kind: AnchorGeneral})
	excluded.Messages = []Message{{Role: RoleUser, Text: "skip"}}
	excluded.Excluded = true
	r.AddThread(Anchor{Kind: AnchorGeneral})

	got := Export(r, "uncommitted changes", ExportOptions{Agent: true})
	want := "# Code review feedback\n\nScope: uncommitted changes\n\n" +
		"Lines are 1-based. (+) marks lines of the new version, (-) lines of the old version. Address each item, or explain why not.\n" +
		"\n## h.go:43-44 (+)\n```\nif err == x {\n}\n```\nuse errors.Is\n" +
		"\n## s.go (file-level)\n\n**Reviewer:**\nwhy remove validation?\n\n**Review agent:**\nIt moved to the caller.\n\n**Reviewer:**\nkeep it here too\n"
	if got != want {
		t.Errorf("Export =\n%s\nwant\n%s", got, want)
	}

	plain := Export(r, "x", ExportOptions{Resolved: true})
	if strings.Contains(plain, "Review agent") || !strings.Contains(plain, "## General\ndone") || strings.Contains(plain, "skip") {
		t.Errorf("Export without agent =\n%s", plain)
	}
}

func TestExportEmpty(t *testing.T) {
	got := Export(New("/r", gitx.Scope{}), "s", ExportOptions{})
	if !strings.HasSuffix(got, "No comments.\n") {
		t.Errorf("Export = %q", got)
	}
}

func TestQuestionPrompt(t *testing.T) {
	th := &Thread{Anchor: Anchor{Kind: AnchorLines, File: "a.go", Side: SideNew, Start: 3, Quote: "x := 1"}}
	th.Messages = []Message{{Role: RoleUser, Text: "why?"}}
	p := QuestionPrompt(th, "why?", false)
	if !strings.Contains(p, "a.go:3 (+)") || !strings.Contains(p, "x := 1") || !strings.HasSuffix(p, "why?") || strings.Contains(p, "so far") {
		t.Errorf("first prompt = %q", p)
	}
	th.Messages = append(th.Messages, Message{Role: RoleAgent, Text: "because"}, Message{Role: RoleUser, Text: "ok?"})
	if p := QuestionPrompt(th, "ok?", true); p != "ok?" {
		t.Errorf("prompt to a known session = %q", p)
	}
	p = QuestionPrompt(th, "ok?", false)
	if !strings.Contains(p, "a.go:3 (+)") || !strings.Contains(p, "Reviewer: why?\n\nAgent: because") || strings.Contains(p, "Reviewer: ok?") || !strings.HasSuffix(p, "ok?") {
		t.Errorf("prompt to a new session = %q", p)
	}
}

func TestThreadCounts(t *testing.T) {
	r := New("/r", gitx.Scope{})
	r.AddThread(Anchor{Kind: AnchorLines, File: "a.go"})
	r.AddThread(Anchor{Kind: AnchorFile, File: "a.go"})
	r.AddThread(Anchor{Kind: AnchorLines, File: "b.go"})
	r.AddThread(Anchor{Kind: AnchorSection, Section: "S"})
	r.AddThread(Anchor{Kind: AnchorGeneral})
	want := map[string]int{"a.go": 2, "b.go": 1}
	if got := r.ThreadCounts(); !reflect.DeepEqual(got, want) {
		t.Errorf("ThreadCounts = %v, want %v", got, want)
	}
}

func TestThreads(t *testing.T) {
	r := New("/r", gitx.Scope{})
	a := r.AddThread(Anchor{Kind: AnchorLines, File: "a.go"})
	r.AddThread(Anchor{Kind: AnchorFile, File: "a.go"})
	r.AddThread(Anchor{Kind: AnchorSection, Section: "S"})
	if n := len(r.ThreadsOn("a.go")); n != 2 {
		t.Errorf("ThreadsOn = %d, want 2", n)
	}
	if n := len(r.ThreadsOnSection("S")); n != 1 {
		t.Errorf("ThreadsOnSection = %d, want 1", n)
	}
	if r.Thread(a.ID) != a {
		t.Error("Thread lookup failed")
	}
	r.DeleteThread(a.ID)
	if r.Thread(a.ID) != nil || len(r.Threads) != 2 {
		t.Error("DeleteThread failed")
	}
	a.Messages = []Message{{Role: RoleUser}, {Role: RoleAgent, Pending: true}}
	if !a.Discussed() {
		t.Error("Discussed wrong")
	}
}

func TestOverviewStale(t *testing.T) {
	o := &Overview{Groups: []Group{{Files: []string{"a", "b"}}}}
	tests := []struct {
		files []string
		want  bool
	}{
		{[]string{"b", "a"}, false},
		{[]string{"a"}, true},
		{[]string{"a", "c"}, true},
		{[]string{"a", "b", "c"}, true},
	}
	for _, tt := range tests {
		if got := o.Stale("", tt.files); got != tt.want {
			t.Errorf("Stale(%v) = %v, want %v", tt.files, got, tt.want)
		}
	}
	var none *Overview
	if none.Stale("", []string{"a"}) || (&Overview{}).Stale("", []string{"a"}) {
		t.Error("overview without groups is stale")
	}
	o.PatchID = "p1"
	if o.Stale("p1", []string{"c"}) || !o.Stale("p2", []string{"a", "b"}) || !o.Stale("", nil) {
		t.Error("an overview with a patch ID does not compare it")
	}
}

func TestDrift(t *testing.T) {
	ov := &Overview{Files: map[string]string{"a.go": "1", "b.go": "2", "c.go": "3", "bin": ""}}
	d := ov.Drift(map[string]string{"a.go": "1", "b.go": "9", "bin": "", "new.go": "4"})
	if d != (Drift{Written: 4, Changed: 2, Gone: 1, New: 1, Known: true}) || d.Disjoint() {
		t.Errorf("drift = %+v", d)
	}
	if d := ov.Drift(map[string]string{"output.json": "5"}); !d.Disjoint() || d.New != 1 {
		t.Errorf("other files: %+v", d)
	}
	if d := ov.Drift(nil); !d.Disjoint() {
		t.Errorf("clean tree: %+v", d)
	}
	old := &Overview{Groups: []Group{{Files: []string{"a.go", "b.go"}}}}
	if d := old.Drift(map[string]string{"a.go": "1"}); d != (Drift{Written: 2, Gone: 1}) {
		t.Errorf("overview without file IDs: %+v", d)
	}
	if d := (&Overview{Markdown: "x"}).Drift(nil); d.Disjoint() {
		t.Errorf("an overview without files is disjoint: %+v", d)
	}
}
