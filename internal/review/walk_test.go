package review

import (
	"reflect"
	"testing"
	"time"

	"github.com/psd-coder/agentdiff/internal/gitx"
)

func TestParseWalkthrough(t *testing.T) {
	answer := "Here it is.\n\n```json\n" + `{
		"summary": " Keys are lower-cased. ",
		"steps": [
			{"title": "Lower-case keys", "text": "Get lower-cases.", "refs": [
				{"file": "./cache/cache.go", "start": 6, "end": 6, "why": "the change"},
				{"file": "cache/cache.go", "side": "old", "start": 4, "end": 2},
				{"file": "cache/cache.go", "start": 1, "end": 40},
				{"file": "", "start": 3, "end": 4},
				{"file": "x.go", "start": 0, "end": 4},
				{"file": "x.go", "start": 9223372036854775800, "end": 9223372036854775807}
			]},
			{"title": " ", "text": ""}
		],
		"checklist": [{"question": "Data?", "answer": "None.", "refs": [{"file": "a.go", "start": 2}]}]
	}` + "\n```\n"
	w, ok := parseWalkthrough(answer)
	if !ok {
		t.Fatal("no walkthrough")
	}
	want := &Walkthrough{
		Summary: "Keys are lower-cased.",
		Steps: []Step{{Title: "Lower-case keys", Text: "Get lower-cases.", Refs: []Ref{
			{File: "cache/cache.go", Side: SideNew, Start: 6, End: 6, Why: "the change"},
			{File: "cache/cache.go", Side: SideOld, Start: 4, End: 4},
			{File: "cache/cache.go", Side: SideNew, Start: 1, End: 15},
		}}},
		Checklist: []CheckItem{{Question: "Data?", Answer: "None.", Refs: []Ref{{File: "a.go", Side: SideNew, Start: 2, End: 2}}}},
	}
	if !reflect.DeepEqual(w, want) {
		t.Errorf("got  %+v\nwant %+v", w, want)
	}
}

func TestParseWalkthroughFallbacks(t *testing.T) {
	if w, ok := parseWalkthrough(`{"summary": "bare", "steps": []}`); !ok || w.Summary != "bare" {
		t.Errorf("bare JSON = %+v, %v", w, ok)
	}
	for _, answer := range []string{"## Markdown only", "```json\n{broken\n```", `{"steps": []}`, ""} {
		if _, ok := parseWalkthrough(answer); ok {
			t.Errorf("parseWalkthrough(%q) found a walkthrough", answer)
		}
	}
}

func TestWalkTree(t *testing.T) {
	r := New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted})
	r.Overview = &Overview{Walk: &Walkthrough{Steps: []Step{{Title: "One"}, {Title: "Two"}}}}
	a := r.AddWalk("", 1, "")
	a.Content = &Walkthrough{Steps: []Step{{Title: "Inner"}}}
	b := r.AddWalk(a.ID, 0, " why? ")
	c := r.AddWalk(b.ID, -1, "and then?")
	other := r.AddWalk("", -1, "general")

	if got := r.WalkTitle(a); got != "Two" {
		t.Errorf("title of a = %q", got)
	}
	if got := r.WalkTitle(b); got != "why?" {
		t.Errorf("title of b = %q", got)
	}
	if s, ok := r.StepOf(b); !ok || s.Title != "Inner" {
		t.Errorf("step of b = %+v, %v", s, ok)
	}
	if _, ok := r.StepOf(other); ok {
		t.Error("a question about the overview has a step")
	}
	if got := r.WalkPath(c.ID); len(got) != 3 || got[0] != a || got[2] != c {
		t.Errorf("path = %+v", got)
	}
	if got := r.WalksOn("", 1); len(got) != 1 || got[0] != a {
		t.Errorf("walks on step 1 = %+v", got)
	}
	if !r.HasStepWalks("") || r.HasStepWalks(c.ID) {
		t.Error("HasStepWalks")
	}
	r.DeleteStepWalks("")
	if len(r.Walks) != 1 || r.Walks[0] != other {
		t.Errorf("walks after deleting the step walks = %+v", r.Walks)
	}
	r.DeleteWalk(other.ID)
	if len(r.Walks) != 0 {
		t.Errorf("walks after delete = %+v", r.Walks)
	}
}

func TestTurns(t *testing.T) {
	w := &Walk{Question: "Why?"}
	w.setAnswer(&Walkthrough{Summary: "Because."}, "", "", time.Unix(1, 0))
	w.More = append(w.More, Turn{Question: "And then?"})
	w.setAnswer(nil, "", "failed", time.Unix(2, 0))
	turns := w.Turns()
	if len(turns) != 2 || turns[0].Content.Summary != "Because." || turns[1].Err != "failed" || w.Err != "" {
		t.Errorf("turns = %+v", turns)
	}
}
