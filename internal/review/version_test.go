package review

import (
	"encoding/json"
	"testing"
)

func TestReviewSavedBeforeVersions(t *testing.T) {
	saved := `{
		"repo": "/repo",
		"scope": {"kind": "uncommitted"},
		"overview": {"agent": "claude", "walk": {"summary": "s", "steps": [{"title": "One"}, {"title": "Two"}]}},
		"walks": [
			{"id": "d", "step": 1},
			{"id": "q", "step": -1, "question": "Why?"},
			{"id": "z", "step": 0, "question": "And?"},
			{"id": "i", "parent": "d", "step": 0}
		]
	}`
	var rev Review
	if err := json.Unmarshal([]byte(saved), &rev); err != nil {
		t.Fatal(err)
	}
	ov := rev.Overview
	if ov.ID == "" || ov.Version != 1 {
		t.Fatalf("overview ID %q, version %d", ov.ID, ov.Version)
	}
	if ov.Walk.Steps[0].ID != "s1" || ov.Walk.Steps[1].ID != "s2" {
		t.Errorf("step IDs = %q, %q", ov.Walk.Steps[0].ID, ov.Walk.Steps[1].ID)
	}
	want := map[string][2]string{"d": {ov.ID, "s2"}, "q": {ov.ID, ""}, "z": {ov.ID, "s1"}, "i": {"d", "s1"}}
	for id, w := range want {
		walk := rev.Walk(id)
		if walk.Parent != w[0] || walk.StepID != w[1] {
			t.Errorf("walk %s: parent %q, step %q, want %q, %q", id, walk.Parent, walk.StepID, w[0], w[1])
		}
	}
	if step, ok := rev.StepOf(rev.Walk("d")); !ok || step.Title != "Two" {
		t.Errorf("step of d = %+v, %v", step, ok)
	}
}

func TestEachRefSkipsVersionsOfOtherChanges(t *testing.T) {
	ref := func(file string) []Step { return []Step{{ID: "s1", Refs: []Ref{{File: file}}}} }
	rev := &Review{
		Overview: &Overview{ID: "v3", PatchID: "p2", Walk: &Walkthrough{Steps: ref("latest.go")}},
		Previous: []*Overview{
			{ID: "v1", PatchID: "p1", Walk: &Walkthrough{Steps: ref("other.go")}},
			{ID: "v2", PatchID: "p2", Walk: &Walkthrough{Steps: ref("same.go")}},
		},
	}
	for parent, file := range map[string]string{"v1": "other-walk.go", "v2": "same-walk.go", "v3": "latest-walk.go"} {
		walk := rev.AddWalk(parent, "s1", "")
		walk.Content = &Walkthrough{Steps: ref(file)}
	}
	got := map[string]bool{}
	rev.EachRef(func(r *Ref) { got[r.File] = true })
	for _, file := range []string{"latest.go", "same.go", "latest-walk.go", "same-walk.go"} {
		if !got[file] {
			t.Errorf("EachRef skipped %s", file)
		}
	}
	for _, file := range []string{"other.go", "other-walk.go"} {
		if got[file] {
			t.Errorf("EachRef gave %s of a version written for other changes", file)
		}
	}
}
