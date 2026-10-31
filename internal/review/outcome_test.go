package review

import "testing"

func TestOutcome(t *testing.T) {
	answered := &Walkthrough{Summary: "s"}
	for _, tc := range []struct {
		name   string
		rev    Review
		target string
		want   Outcome
	}{
		{name: "no overview", target: TargetOverview, want: OutcomeNone},
		{name: "an empty overview", rev: Review{Overview: &Overview{}}, target: TargetOverview, want: OutcomeNone},
		{name: "a written overview", rev: Review{Overview: &Overview{Markdown: "m"}}, target: TargetOverview, want: OutcomeAnswered},
		{name: "an overview that failed over an earlier one", rev: Review{Overview: &Overview{Walk: answered, Err: "boom"}}, target: TargetOverview, want: OutcomeFailed},
		{name: "a stopped overview", rev: Review{Overview: &Overview{Err: Stopped}}, target: TargetOverview, want: OutcomeStopped},
		{name: "a walk not written yet", rev: Review{Walks: []*Walk{{ID: "w"}}}, target: "w", want: OutcomeNone},
		{name: "a written walk", rev: Review{Walks: []*Walk{{ID: "w", Content: answered}}}, target: "w", want: OutcomeAnswered},
		{
			name:   "a follow-up answered after a failed first answer",
			rev:    Review{Walks: []*Walk{{ID: "w", Err: "boom", More: []Turn{{Question: "q", Markdown: "m"}}}}},
			target: "w", want: OutcomeAnswered,
		},
		{
			name:   "a failed follow-up after a good first answer",
			rev:    Review{Walks: []*Walk{{ID: "w", Content: answered, More: []Turn{{Question: "q", Err: "boom"}}}}},
			target: "w", want: OutcomeFailed,
		},
		{
			name:   "a stopped follow-up",
			rev:    Review{Walks: []*Walk{{ID: "w", Content: answered, More: []Turn{{Question: "q", Err: Stopped}}}}},
			target: "w", want: OutcomeStopped,
		},
		{name: "a plain comment", rev: Review{Threads: []*Thread{{ID: "t", Messages: []Message{{Role: RoleUser, Text: "c"}}}}}, target: "t", want: OutcomeNone},
		{
			name:   "a thread whose latest answer failed, with a comment after it",
			rev:    Review{Threads: []*Thread{{ID: "t", Messages: []Message{{Role: RoleUser}, {Role: RoleAgent, Text: "a"}, {Role: RoleUser}, {Role: RoleAgent, Err: "boom"}, {Role: RoleUser}}}}},
			target: "t", want: OutcomeFailed,
		},
		{
			name:   "a thread answered after a failure",
			rev:    Review{Threads: []*Thread{{ID: "t", Messages: []Message{{Role: RoleUser}, {Role: RoleAgent, Err: "boom"}, {Role: RoleUser}, {Role: RoleAgent, Text: "a"}}}}},
			target: "t", want: OutcomeAnswered,
		},
		{
			name:   "a stopped thread",
			rev:    Review{Threads: []*Thread{{ID: "t", Messages: []Message{{Role: RoleUser}, {Role: RoleAgent, Err: Stopped}}}}},
			target: "t", want: OutcomeStopped,
		},
		{name: "an unknown target", rev: Review{Overview: &Overview{Markdown: "m"}}, target: "gone", want: OutcomeNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rev.Outcome(tc.target); got != tc.want {
				t.Errorf("Outcome = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	r := Review{Walks: []*Walk{{ID: "w"}}, Threads: []*Thread{{ID: "t"}}}
	for _, tc := range []struct {
		id   string
		kind Kind
		ok   bool
	}{
		{TargetOverview, KindOverview, true},
		{"w", KindWalk, true},
		{"t", KindThread, true},
		{"gone", 0, false},
	} {
		if kind, ok := r.Resolve(tc.id); kind != tc.kind || ok != tc.ok {
			t.Errorf("Resolve(%q) = %d, %v; want %d, %v", tc.id, kind, ok, tc.kind, tc.ok)
		}
	}
}
