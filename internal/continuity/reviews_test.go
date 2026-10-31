package continuity

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

const repo = "/repo"

var (
	uncommitted = gitx.Scope{Kind: gitx.ScopeUncommitted}
	commit      = gitx.Scope{Kind: gitx.ScopeCommit, Commit: "c1"}
	archived    = gitx.ArchivedScope("pb")

	// The current review was written for a.go with patch pa.
	same     = gitx.Fingerprint{Patch: "pa", Files: map[string]string{"a.go": "ida"}}
	overlaps = gitx.Fingerprint{Patch: "pc", Files: map[string]string{"a.go": "idx", "c.go": "idc"}}
	disjoint = gitx.Fingerprint{Patch: "pb", Files: map[string]string{"b.go": "idb"}}
)

// saved is a review with a done overview, labelled by its Markdown.
func saved(scope gitx.Scope, label, patch string, files map[string]string) *review.Review {
	r := review.New(repo, scope)
	r.Overview = &review.Overview{Markdown: label, PatchID: patch, Files: files, Generated: time.Unix(1, 0)}
	return r
}

func current(scope gitx.Scope) *review.Review {
	return saved(scope, "current", "pa", map[string]string{"a.go": "ida"})
}

func removeReview(t *testing.T, s store.Store, scope gitx.Scope) {
	t.Helper()
	if err := s.DeleteReview(repo, scope); err != nil {
		t.Fatal(err)
	}
}

func withViewed(r *review.Review, paths ...string) *review.Review {
	for _, p := range paths {
		r.Viewed[p] = true
	}
	return r
}

func viewedFiles(r *review.Review) []string {
	return slices.Sorted(maps.Keys(r.Viewed))
}

// archivedReviews are n archived reviews with the ids a0, a1 and so on.
func archivedReviews(n int) []*review.Review {
	var out []*review.Review
	for i := range n {
		id := fmt.Sprint("a", i)
		out = append(out, saved(gitx.ArchivedScope(id), id, id, nil))
	}
	return out
}

func label(r *review.Review) string {
	if r == nil {
		return ""
	}
	if r.Overview == nil {
		return "empty"
	}
	return r.Overview.Markdown
}

// TestDecide pins what a load does with the review of its scope, one row
// for each cell of the decision.
func TestDecide(t *testing.T) {
	for _, tc := range []struct {
		name string
		// scope is the loaded scope. cur is its review, nil for an empty
		// one, and unsaved leaves it out of the store.
		scope   gitx.Scope
		cur     *review.Review
		unsaved bool
		others  []*review.Review
		// open are the scopes whose reviews the app has open.
		open  []gitx.Scope
		fp    gitx.Fingerprint
		fpErr error
		busy  []string
		// viewed are the files the review of the scope marked viewed.
		viewed    []string
		restore   bool
		relaunch  bool
		startOver bool
		afterRead func(t *testing.T, rs *Reviews, s store.Store)

		rev, archived string
		dropped       []string
		earlier       []string
		// listed is the length of the list of earlier reviews, for a row
		// that checks only the length.
		listed     int
		from       gitx.ScopeKind
		gone       []gitx.Scope
		stored     []gitx.Scope
		wantViewed []string
		err        bool
	}{
		{
			name:  "an empty review moves an archived review with the same changes here",
			scope: uncommitted, others: []*review.Review{saved(archived, "earlier", "pb", nil)}, fp: disjoint,
			rev: "earlier", dropped: []string{"empty"}, from: gitx.ScopeArchived, gone: []gitx.Scope{archived},
		},
		{
			name:  "an empty review copies a commit review with the same changes",
			scope: uncommitted, others: []*review.Review{saved(commit, "commit", "pb", nil)}, fp: disjoint,
			rev: "commit", dropped: []string{"empty"}, from: gitx.ScopeCommit,
		},
		{
			name:  "an empty commit review moves the uncommitted review with the same changes here, even while the changes stay uncommitted as after cherry-pick -n",
			scope: commit, others: []*review.Review{saved(uncommitted, "work", "pb", nil)}, fp: disjoint,
			rev: "work", dropped: []string{"empty"}, from: gitx.ScopeUncommitted, gone: []gitx.Scope{uncommitted},
		},
		{
			name:  "an open source review is read again after its last save",
			scope: commit, others: []*review.Review{saved(uncommitted, "work", "pb", nil)}, open: []gitx.Scope{uncommitted}, fp: disjoint,
			afterRead: func(t *testing.T, rs *Reviews, s store.Store) {
				src := rs.Of(repo, uncommitted)
				src.Overview.Markdown = "saved later"
				if err := s.SaveReview(src); err != nil {
					t.Fatal(err)
				}
			},
			rev: "saved later", dropped: []string{"saved later", "empty"}, from: gitx.ScopeUncommitted, gone: []gitx.Scope{uncommitted},
		},
		{
			name:  "an empty review leaves a source review that an agent writes into",
			scope: commit, others: []*review.Review{saved(uncommitted, "work", "pb", nil)}, open: []gitx.Scope{uncommitted}, fp: disjoint, busy: []string{"work"},
			rev: "empty",
		},
		{
			name:  "a review whose files are all gone stays while an agent writes into the source review",
			scope: uncommitted, cur: current(uncommitted), others: []*review.Review{saved(commit, "commit", "pb", nil)}, open: []gitx.Scope{commit}, fp: disjoint, busy: []string{"commit"},
			rev: "current",
		},
		{
			name:  "an overview that finishes during the load for the current changes stays over the same changes elsewhere",
			scope: uncommitted, others: []*review.Review{saved(commit, "commit", "pb", nil)}, fp: disjoint,
			afterRead: func(_ *testing.T, rs *Reviews, _ store.Store) {
				rs.Of(repo, uncommitted).Overview = &review.Overview{Markdown: "fresh", PatchID: "pb", Files: map[string]string{"b.go": "idb"}}
			},
			rev: "fresh",
		},
		{
			name:  "an empty review keeps its viewed files when it takes a review over",
			scope: uncommitted, others: []*review.Review{withViewed(saved(archived, "earlier", "pb", nil), "c.go")}, fp: disjoint, viewed: []string{"b.go"},
			rev: "earlier", dropped: []string{"empty"}, from: gitx.ScopeArchived, gone: []gitx.Scope{archived}, wantViewed: []string{"b.go", "c.go"},
		},
		{
			name:  "a started over review takes nothing over until the app quits",
			scope: uncommitted, cur: current(uncommitted), others: []*review.Review{saved(archived, "earlier", "pb", nil)}, fp: disjoint, startOver: true,
			rev: "empty", earlier: []string{"pb"},
		},
		{
			name:  "an archived review replaces the earlier one with the same id on the list",
			scope: uncommitted, cur: current(uncommitted), others: []*review.Review{saved(gitx.ArchivedScope("pa"), "older", "pa", nil)}, fp: disjoint,
			rev: "empty", archived: "current", dropped: []string{"current"}, earlier: []string{"pa"},
		},
		{
			name:  "the list of earlier reviews holds only the reviews the archive keeps",
			scope: uncommitted, cur: current(uncommitted), others: archivedReviews(20), fp: disjoint,
			rev: "empty", archived: "current", dropped: []string{"current"}, listed: 20,
		},
		{
			name:  "a drifted review an agent writes into stays over the same changes elsewhere",
			scope: uncommitted, cur: current(uncommitted), others: []*review.Review{saved(commit, "commit", "pb", nil)}, fp: disjoint, busy: []string{"current"},
			rev: "current",
		},
		{
			name:  "a drifted review goes to the archive for the same changes elsewhere, even while some of its files are still changed",
			scope: uncommitted, cur: current(uncommitted), others: []*review.Review{saved(commit, "commit", "pc", nil)}, fp: overlaps,
			rev: "commit", archived: "current", dropped: []string{"current", "empty"}, earlier: []string{"pa"}, from: gitx.ScopeCommit,
		},
		{
			name:  "a review whose files are all gone goes to the archive",
			scope: uncommitted, cur: current(uncommitted), fp: disjoint,
			rev: "empty", archived: "current", dropped: []string{"current"}, earlier: []string{"pa"},
		},
		{
			name:  "a review whose files are all gone stays while an agent writes into it",
			scope: uncommitted, cur: current(uncommitted), fp: disjoint, busy: []string{"current"},
			rev: "current",
		},
		{
			name:  "a review stays while some of its files are still changed",
			scope: uncommitted, cur: current(uncommitted), fp: overlaps,
			rev: "current",
		},
		{
			name:  "a review stays while its changes are the same",
			scope: uncommitted, cur: current(uncommitted), fp: same,
			rev: "current",
		},
		{
			name:  "a restored review stays when its files are all gone",
			scope: uncommitted, others: []*review.Review{saved(archived, "earlier", "pa", map[string]string{"a.go": "ida"})}, fp: disjoint, restore: true,
			rev: "earlier",
		},
		{
			name:  "a restored review stays after a relaunch",
			scope: uncommitted, others: []*review.Review{saved(archived, "earlier", "pa", map[string]string{"a.go": "ida"})}, fp: disjoint, restore: true, relaunch: true,
			rev: "earlier",
		},
		{
			name:  "a review without a finished overview stays when its files are all gone",
			scope: uncommitted, cur: &review.Review{Repo: repo, Scope: uncommitted, Overview: &review.Overview{Err: "failed", PatchID: "pa", Files: map[string]string{"a.go": "ida"}}}, fp: disjoint,
			rev: "",
		},
		{
			name:  "a review stays and nothing is taken over when the changes cannot be identified",
			scope: uncommitted, cur: current(uncommitted), others: []*review.Review{saved(archived, "earlier", "pb", nil)}, fp: disjoint, fpErr: errors.New("patch-id"),
			rev: "current", earlier: []string{"pb"},
		},
		{
			name:  "a commit review that is not empty keeps itself over the same changes elsewhere",
			scope: commit, cur: current(commit), others: []*review.Review{saved(uncommitted, "work", "pb", nil)}, fp: disjoint,
			rev: "current",
		},
		{
			name:  "an empty review takes nothing over when the changes are empty",
			scope: uncommitted, others: []*review.Review{saved(commit, "commit", "", nil)}, fp: gitx.Fingerprint{Files: map[string]string{}},
			rev: "empty",
		},
		{
			name:  "a failed move keeps the empty review and reports the error",
			scope: uncommitted, others: []*review.Review{saved(archived, "earlier", "pb", nil)}, fp: disjoint,
			afterRead: func(t *testing.T, _ *Reviews, s store.Store) { removeReview(t, s, archived) },
			rev:       "empty", earlier: []string{"pb"}, err: true,
		},
		{
			name:  "a failed copy brings the review back from the archive and reports the error",
			scope: uncommitted, cur: current(uncommitted), others: []*review.Review{saved(commit, "commit", "pb", nil)}, fp: disjoint,
			afterRead: func(t *testing.T, _ *Reviews, s store.Store) { removeReview(t, s, commit) },
			rev:       "current", stored: []gitx.Scope{uncommitted}, err: true,
		},
		{
			name:  "a failed archive keeps the review and reports the error",
			scope: uncommitted, cur: current(uncommitted), unsaved: true, fp: disjoint,
			rev: "current", err: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := store.Store{Dir: t.TempDir()}
			for _, r := range tc.others {
				if err := s.SaveReview(r); err != nil {
					t.Fatal(err)
				}
			}
			if tc.cur != nil && !tc.unsaved {
				if err := s.SaveReview(tc.cur); err != nil {
					t.Fatal(err)
				}
			}
			rs := New(s)
			for _, sc := range tc.open {
				rs.Of(repo, sc)
			}
			busy := func(r *review.Review) bool { return slices.Contains(tc.busy, label(r)) }
			cur := rs.Of(repo, tc.scope)
			if tc.unsaved {
				cur.Overview = tc.cur.Overview
			}
			for _, path := range tc.viewed {
				cur.Viewed[path] = true
			}
			if tc.startOver {
				var err error
				if cur, err = rs.StartOver(repo, tc.scope); err != nil {
					t.Fatal(err)
				}
			}
			if tc.restore {
				rs.Decide(rs.Read(repo, tc.scope, BeforeLoad(cur), gitx.Fingerprint{Patch: "pz"}, nil), cur, busy)
				out := rs.Restore(rs.Earlier()[0], cur, busy)
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				cur = out.Rev
				if tc.relaunch {
					rs = New(s)
					cur = rs.Of(repo, tc.scope)
				}
			}

			found := rs.Read(repo, tc.scope, BeforeLoad(cur), tc.fp, tc.fpErr)
			if tc.afterRead != nil {
				tc.afterRead(t, rs, s)
			}
			out := rs.Decide(found, cur, busy)

			if got := label(out.Rev); got != tc.rev {
				t.Errorf("review = %q, want %q", got, tc.rev)
			}
			if got := label(out.Archived); got != tc.archived {
				t.Errorf("archived = %q, want %q", got, tc.archived)
			}
			var dropped, earlier []string
			for _, r := range out.Dropped {
				dropped = append(dropped, label(r))
			}
			for _, e := range rs.Earlier() {
				earlier = append(earlier, e.ID())
			}
			if !slices.Equal(dropped, tc.dropped) {
				t.Errorf("dropped = %q, want %q", dropped, tc.dropped)
			}
			if tc.listed > 0 {
				if len(earlier) != tc.listed || len(slices.Compact(slices.Sorted(slices.Values(earlier)))) != tc.listed || earlier[0] != "pa" {
					t.Errorf("earlier = %q, want %d different reviews, the archived one first", earlier, tc.listed)
				}
			} else if !slices.Equal(earlier, tc.earlier) {
				t.Errorf("earlier = %q, want %q", earlier, tc.earlier)
			}
			var from gitx.ScopeKind
			if f := rs.AdoptedFrom(); f != nil {
				from = f.Kind
			}
			if from != tc.from {
				t.Errorf("adopted from %q, want %q", from, tc.from)
			}
			if (out.Err != nil) != tc.err {
				t.Errorf("err = %v, want error %v", out.Err, tc.err)
			}
			for _, sc := range tc.gone {
				if !s.LoadReview(repo, sc).Empty() {
					t.Errorf("the review of %s stayed in the store", sc.Key())
				}
			}
			for _, sc := range tc.stored {
				if s.LoadReview(repo, sc).Empty() {
					t.Errorf("the review of %s is gone from the store", sc.Key())
				}
			}
			if tc.wantViewed != nil {
				if got := viewedFiles(out.Rev); !slices.Equal(got, tc.wantViewed) {
					t.Errorf("viewed = %q, want %q", got, tc.wantViewed)
				}
				if got := viewedFiles(s.LoadReview(repo, tc.scope)); !slices.Equal(got, tc.wantViewed) {
					t.Errorf("saved viewed = %q, want %q", got, tc.wantViewed)
				}
			}
			if rs.Obsolete(out.Rev) || rs.Obsolete(cur) != (out.Rev != cur) {
				t.Errorf("obsolete: new %v, old %v", rs.Obsolete(out.Rev), rs.Obsolete(cur))
			}
		})
	}
}

// TestLocated puts the commits found into the archived reviews, and
// names the commit of the review of the scope only while it stays there.
func TestLocated(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fp      gitx.Fingerprint
		movedTo string
	}{
		{"the drifted review stays", overlaps, "c2"},
		{"the drifted review went to the archive", disjoint, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := store.Store{Dir: t.TempDir()}
			for _, r := range []*review.Review{current(uncommitted), saved(gitx.ArchivedScope("pq"), "earlier", "pq", nil)} {
				if err := s.SaveReview(r); err != nil {
					t.Fatal(err)
				}
			}
			rs := New(s)
			cur := rs.Of(repo, uncommitted)
			found := rs.Read(repo, uncommitted, BeforeLoad(cur), tc.fp, nil)
			if !found.Searches() {
				t.Fatal("nothing to look for")
			}
			rs.Decide(found, cur, func(*review.Review) bool { return false })
			movedTo := rs.Located(found, map[string]string{"": "c2", "pq": "c3"}, map[string]string{"pq": "c4"})
			if movedTo != tc.movedTo {
				t.Errorf("moved to %q, want %q", movedTo, tc.movedTo)
			}
			e := rs.Find("pq")
			if e == nil || e.MovedTo != "c3" || e.Probably != "c4" {
				t.Errorf("earlier review %+v, want moved to c3, probably in c4", e)
			}
		})
	}
}

// TestDecideAfterRun archives the review and takes the source over on the
// first load after the agent run on the source ends.
func TestDecideAfterRun(t *testing.T) {
	s := store.Store{Dir: t.TempDir()}
	for _, r := range []*review.Review{current(uncommitted), saved(commit, "commit", "pb", nil)} {
		if err := s.SaveReview(r); err != nil {
			t.Fatal(err)
		}
	}
	rs := New(s)
	src := rs.Of(repo, commit)
	running := true
	busy := func(r *review.Review) bool { return running && r == src }
	load := func() Outcome {
		cur := rs.Of(repo, uncommitted)
		return rs.Decide(rs.Read(repo, uncommitted, BeforeLoad(cur), disjoint, nil), cur, busy)
	}
	if out := load(); label(out.Rev) != "current" || out.Archived != nil {
		t.Fatalf("during the run: review %q, archived %q", label(out.Rev), label(out.Archived))
	}
	running = false
	if out := load(); label(out.Rev) != "commit" || label(out.Archived) != "current" || out.Err != nil {
		t.Errorf("after the run: review %q, archived %q, err %v", label(out.Rev), label(out.Archived), out.Err)
	}
}

// TestMoveTo moves an archived review into a commit only while the commit
// has no review, neither saved nor open.
func TestMoveTo(t *testing.T) {
	for _, tc := range []struct {
		name string
		// saved is the saved review of the commit, and open edits the
		// commit's review in memory.
		saved *review.Review
		open  func(r *review.Review)
		busy  bool
		moved bool
	}{
		{name: "moves into a commit without a review", moved: true},
		{name: "moves into a commit whose open review is empty", open: func(*review.Review) {}, moved: true},
		{name: "leaves a commit with a saved review", saved: saved(commit, "commit", "pc", nil)},
		{name: "leaves a commit whose open review is not saved yet", open: func(r *review.Review) { r.Overview = &review.Overview{Markdown: "unsaved"} }},
		{name: "leaves a commit whose open review an agent writes into", open: func(*review.Review) {}, busy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := store.Store{Dir: t.TempDir()}
			for _, r := range []*review.Review{saved(archived, "earlier", "pb", nil), tc.saved} {
				if r == nil {
					continue
				}
				if err := s.SaveReview(r); err != nil {
					t.Fatal(err)
				}
			}
			rs := New(s)
			var open *review.Review
			if tc.open != nil {
				open = rs.Of(repo, commit)
				tc.open(open)
			}
			cur := rs.Of(repo, uncommitted)
			rs.Decide(rs.Read(repo, uncommitted, BeforeLoad(cur), same, nil), cur, func(*review.Review) bool { return false })
			busy := func(r *review.Review) bool { return tc.busy && r == open }

			moved, err := rs.MoveTo(repo, rs.Find("pb"), commit, busy)
			if err != nil {
				t.Fatal(err)
			}
			if moved != tc.moved {
				t.Fatalf("moved = %v, want %v", moved, tc.moved)
			}
			if got := label(s.LoadReview(repo, commit)) == "earlier"; got != tc.moved {
				t.Errorf("the commit holds the earlier review: %v", got)
			}
			if got := rs.Find("pb") == nil; got != tc.moved {
				t.Errorf("the earlier review left the list: %v", got)
			}
			if open != nil && rs.Obsolete(open) != tc.moved {
				t.Errorf("the open review is obsolete: %v", rs.Obsolete(open))
			}
		})
	}
}
