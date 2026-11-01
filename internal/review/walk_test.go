package review

import (
	"encoding/json"
	"reflect"
	"strings"
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
		"sections": [
			{"title": " Risks ", "items": [{"title": "Data?", "text": "None.", "refs": [{"file": "a.go", "start": 2}]}, {"title": "", "text": " "}]},
			{"title": "Empty", "items": [{"title": " ", "text": ""}]}
		]
	}` + "\n```\n"
	walkthrough, ok := parseWalkthrough(answer)
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
		Sections: []Section{{Title: "Risks", Items: []Step{{Title: "Data?", Text: "None.", Refs: []Ref{{File: "a.go", Side: SideNew, Start: 2, End: 2}}}}}},
	}
	if !reflect.DeepEqual(walkthrough, want) {
		t.Errorf("got  %+v\nwant %+v", walkthrough, want)
	}
}

func TestSavedChecklistBecomesTheFirstSection(t *testing.T) {
	saved := `{"summary": "s", "steps": [], "checklist": [{"question": "Data?", "answer": "None.", "refs": [{"file": "a.go", "side": "new", "start": 2, "end": 3}]}], "sections": [{"title": "Risks", "items": [{"title": "Cache", "text": "Stale."}]}]}`
	var walkthrough Walkthrough
	if err := json.Unmarshal([]byte(saved), &walkthrough); err != nil {
		t.Fatal(err)
	}
	want := []Section{
		{Title: "Checklist", Items: []Step{{Title: "Data?", Text: "None.", Refs: []Ref{{File: "a.go", Side: SideNew, Start: 2, End: 3}}}}},
		{Title: "Risks", Items: []Step{{Title: "Cache", Text: "Stale."}}},
	}
	if !reflect.DeepEqual(walkthrough.Sections, want) {
		t.Errorf("sections = %+v\nwant %+v", walkthrough.Sections, want)
	}
	out, err := json.Marshal(walkthrough)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "checklist") {
		t.Errorf("a saved walkthrough still writes the checklist: %s", out)
	}
}

func TestParseWalkthroughFallbacks(t *testing.T) {
	if walkthrough, ok := parseWalkthrough(`{"summary": "bare", "steps": []}`); !ok || walkthrough.Summary != "bare" {
		t.Errorf("bare JSON = %+v, %v", walkthrough, ok)
	}
	for _, answer := range []string{"## Markdown only", "```json\n{broken\n```", `{"steps": []}`, ""} {
		if _, ok := parseWalkthrough(answer); ok {
			t.Errorf("parseWalkthrough(%q) found a walkthrough", answer)
		}
	}
}

func TestWalkTree(t *testing.T) {
	review := New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted})
	review.Overview = &Overview{Walk: &Walkthrough{Steps: []Step{{Title: "One"}, {Title: "Two"}}}}
	deeper := review.AddWalk("", 1, "")
	deeper.Content = &Walkthrough{Steps: []Step{{Title: "Inner"}}}
	question := review.AddWalk(deeper.ID, 0, " why? ")
	followUp := review.AddWalk(question.ID, -1, "and then?")
	other := review.AddWalk("", -1, "general")

	if got := review.WalkTitle(deeper); got != "Two" {
		t.Errorf("title of a = %q", got)
	}
	if got := review.WalkTitle(question); got != "why?" {
		t.Errorf("title of b = %q", got)
	}
	if step, ok := review.StepOf(question); !ok || step.Title != "Inner" {
		t.Errorf("step of b = %+v, %v", step, ok)
	}
	if _, ok := review.StepOf(other); ok {
		t.Error("a question about the overview has a step")
	}
	if got := review.WalkPath(followUp.ID); len(got) != 3 || got[0] != deeper || got[2] != followUp {
		t.Errorf("path = %+v", got)
	}
	if got := review.WalksOn("", 1); len(got) != 1 || got[0] != deeper {
		t.Errorf("walks on step 1 = %+v", got)
	}
	if !review.HasStepWalks("") || review.HasStepWalks(followUp.ID) {
		t.Error("HasStepWalks")
	}
	review.DeleteStepWalks("")
	if len(review.Walks) != 1 || review.Walks[0] != other {
		t.Errorf("walks after deleting the step walks = %+v", review.Walks)
	}
	review.DeleteWalk(other.ID)
	if len(review.Walks) != 0 {
		t.Errorf("walks after delete = %+v", review.Walks)
	}
}

func TestTurns(t *testing.T) {
	walk := &Walk{Question: "Why?"}
	walk.setAnswer(&Walkthrough{Summary: "Because."}, "", "", time.Unix(1, 0))
	walk.More = append(walk.More, Turn{Question: "And then?"})
	walk.setAnswer(nil, "", "failed", time.Unix(2, 0))
	turns := walk.Turns()
	if len(turns) != 2 || turns[0].Content.Summary != "Because." || turns[1].Err != "failed" || walk.Err != "" {
		t.Errorf("turns = %+v", turns)
	}
}
