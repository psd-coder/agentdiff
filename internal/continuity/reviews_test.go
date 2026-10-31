package continuity

import (
	"errors"
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

func label(r *review.Review) string {
	if r == nil {
		return ""
	}
	if r.Overview == nil {
		return "empty"
	}
	return r.Overview.Markdown
}

// TestDecide pins what a load does today with the review of its scope,
// one row for each cell of the decision.
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
		open      []gitx.Scope
		fp        gitx.Fingerprint
		fpErr     error
		busy      []string
		restore   bool
		afterRead func(t *testing.T, rs *Reviews, s store.Store)

		rev, archived string
		dropped       []string
		earlier       []string
		from          gitx.ScopeKind
		gone          []gitx.Scope
		err           bool
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
			name:  "an empty commit review moves the uncommitted review with the same changes here",
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
			name:  "a review whose files are all gone goes to the archive even when the source an agent writes into stays",
			scope: uncommitted, cur: current(uncommitted), others: []*review.Review{saved(commit, "commit", "pb", nil)}, open: []gitx.Scope{commit}, fp: disjoint, busy: []string{"commit"},
			rev: "empty", archived: "current", dropped: []string{"current"}, earlier: []string{"pa"},
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
			name:  "a failed copy after the archive leaves the scope empty and reports the error",
			scope: uncommitted, cur: current(uncommitted), others: []*review.Review{saved(commit, "commit", "pb", nil)}, fp: disjoint,
			afterRead: func(t *testing.T, _ *Reviews, s store.Store) { removeReview(t, s, commit) },
			rev:       "empty", archived: "current", dropped: []string{"current"}, earlier: []string{"pa"}, err: true,
		},
		{
			name:  "a failed archive keeps the review and reports the error",
			scope: uncommitted, cur: current(uncommitted), unsaved: true, fp: disjoint,
			rev: "current", err: true,
		},
	} {
		t.Run("today: "+tc.name, func(t *testing.T) {
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
			if tc.restore {
				rs.Decide(rs.Read(repo, tc.scope, BeforeLoad(cur), gitx.Fingerprint{Patch: "pz"}, nil), cur, busy)
				out := rs.Restore(rs.Earlier()[0], cur, busy)
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				cur = out.Rev
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
			if !slices.Equal(earlier, tc.earlier) {
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
