package continuity

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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
	rev := review.New(repo, scope)
	rev.Overview = &review.Overview{Markdown: label, PatchID: patch, Files: files, Generated: time.Unix(1, 0)}
	return rev
}

// load is a load of scope, whose review is cur, that read the changes
// with the fingerprint fp.
func load(rs *Reviews, scope gitx.Scope, cur *review.Review, fp gitx.Fingerprint) *Load {
	scopeLoad := rs.Begin(repo, scope, cur)
	scopeLoad.Read(fp, nil)
	return scopeLoad
}

func current(scope gitx.Scope) *review.Review {
	return saved(scope, "current", "pa", map[string]string{"a.go": "ida"})
}

func removeReview(t *testing.T, storage store.Store, scope gitx.Scope) {
	t.Helper()
	if err := storage.DeleteReview(repo, scope); err != nil {
		t.Fatal(err)
	}
}

func withViewed(rev *review.Review, paths ...string) *review.Review {
	for _, p := range paths {
		rev.Viewed[p] = true
	}
	return rev
}

func viewedFiles(rev *review.Review) []string {
	return slices.Sorted(maps.Keys(rev.Viewed))
}

// archivedReviews are n archived reviews with the ids a0, a1 and so on.
func archivedReviews(count int) []*review.Review {
	var out []*review.Review
	for i := range count {
		id := fmt.Sprint("a", i)
		out = append(out, saved(gitx.ArchivedScope(id), id, id, nil))
	}
	return out
}

func label(rev *review.Review) string {
	if rev == nil {
		return ""
	}
	if rev.Overview == nil {
		return "empty"
	}
	return rev.Overview.Markdown
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
		afterRead func(subtest *testing.T, rs *Reviews, storage store.Store)

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
			storage := store.Store{Dir: t.TempDir()}
			for _, r := range tc.others {
				if err := storage.SaveReview(r); err != nil {
					t.Fatal(err)
				}
			}
			if tc.cur != nil && !tc.unsaved {
				if err := storage.SaveReview(tc.cur); err != nil {
					t.Fatal(err)
				}
			}
			rs := New(storage)
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
				load(rs, tc.scope, cur, gitx.Fingerprint{Patch: "pz"}).Decide(cur, busy)
				out := rs.Restore(rs.Earlier()[0], cur, busy)
				if out.Err != nil {
					t.Fatal(out.Err)
				}
				cur = out.Rev
				if tc.relaunch {
					rs = New(storage)
					cur = rs.Of(repo, tc.scope)
				}
			}

			scopeLoad := rs.Begin(repo, tc.scope, cur)
			scopeLoad.Read(tc.fp, tc.fpErr)
			if tc.afterRead != nil {
				tc.afterRead(t, rs, storage)
			}
			out := scopeLoad.Decide(cur, busy)

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
			if adoptedFrom := rs.AdoptedFrom(); adoptedFrom != nil {
				from = adoptedFrom.Kind
			}
			if from != tc.from {
				t.Errorf("adopted from %q, want %q", from, tc.from)
			}
			if (out.Err != nil) != tc.err {
				t.Errorf("err = %v, want error %v", out.Err, tc.err)
			}
			for _, sc := range tc.gone {
				if !storage.LoadReview(repo, sc).Empty() {
					t.Errorf("the review of %s stayed in the store", sc.Key())
				}
			}
			for _, sc := range tc.stored {
				if storage.LoadReview(repo, sc).Empty() {
					t.Errorf("the review of %s is gone from the store", sc.Key())
				}
			}
			if tc.wantViewed != nil {
				if got := viewedFiles(out.Rev); !slices.Equal(got, tc.wantViewed) {
					t.Errorf("viewed = %q, want %q", got, tc.wantViewed)
				}
				if got := viewedFiles(storage.LoadReview(repo, tc.scope)); !slices.Equal(got, tc.wantViewed) {
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
			storage := store.Store{Dir: t.TempDir()}
			for _, r := range []*review.Review{current(uncommitted), saved(gitx.ArchivedScope("pq"), "earlier", "pq", nil)} {
				if err := storage.SaveReview(r); err != nil {
					t.Fatal(err)
				}
			}
			rs := New(storage)
			cur := rs.Of(repo, uncommitted)
			scopeLoad := load(rs, uncommitted, cur, tc.fp)
			scopeLoad.Decide(cur, func(*review.Review) bool { return false })
			movedTo := scopeLoad.located(map[string]string{"": "c2", "pq": "c3"}, map[string]string{"pq": "c4"})
			if movedTo != tc.movedTo {
				t.Errorf("moved to %q, want %q", movedTo, tc.movedTo)
			}
			earlier := rs.Find("pq")
			if earlier == nil || earlier.MovedTo != "c3" || earlier.Probably != "c4" {
				t.Errorf("earlier review %+v, want moved to c3, probably in c4", earlier)
			}
		})
	}
}

// TestDecideAfterRun archives the review and takes the source over on the
// first load after the agent run on the source ends.
func TestDecideAfterRun(t *testing.T) {
	storage := store.Store{Dir: t.TempDir()}
	for _, r := range []*review.Review{current(uncommitted), saved(commit, "commit", "pb", nil)} {
		if err := storage.SaveReview(r); err != nil {
			t.Fatal(err)
		}
	}
	rs := New(storage)
	src := rs.Of(repo, commit)
	running := true
	busy := func(r *review.Review) bool { return running && r == src }
	load := func() Outcome {
		cur := rs.Of(repo, uncommitted)
		return load(rs, uncommitted, cur, disjoint).Decide(cur, busy)
	}
	if out := load(); label(out.Rev) != "current" || out.Archived != nil {
		t.Fatalf("during the run: review %q, archived %q", label(out.Rev), label(out.Archived))
	}
	running = false
	if out := load(); label(out.Rev) != "commit" || label(out.Archived) != "current" || out.Err != nil {
		t.Errorf("after the run: review %q, archived %q, err %v", label(out.Rev), label(out.Archived), out.Err)
	}
}

// TestArchiveComesBack follows the review of the uncommitted changes
// while its changes go away and come back: it goes to the archive, comes
// back with its changes, and once restored stays without them.
func TestArchiveComesBack(t *testing.T) {
	storage := store.Store{Dir: t.TempDir()}
	if err := storage.SaveReview(current(uncommitted)); err != nil {
		t.Fatal(err)
	}
	rs := New(storage)
	never := func(*review.Review) bool { return false }
	reload := func(fp gitx.Fingerprint) Outcome {
		t.Helper()
		cur := rs.Of(repo, uncommitted)
		out := load(rs, uncommitted, cur, fp).Decide(cur, never)
		if out.Err != nil {
			t.Fatal(out.Err)
		}
		return out
	}
	ids := func() []string {
		var out []string
		for _, e := range rs.Earlier() {
			out = append(out, e.ID())
		}
		return out
	}

	if out := reload(disjoint); label(out.Archived) != "current" || label(out.Rev) != "empty" || !slices.Equal(ids(), []string{"pa"}) {
		t.Fatalf("other changes: review %q, archived %q, earlier %q", label(out.Rev), label(out.Archived), ids())
	}
	if out := reload(same); label(out.Rev) != "current" || len(ids()) != 0 || rs.AdoptedFrom() == nil || rs.AdoptedFrom().Kind != gitx.ScopeArchived {
		t.Fatalf("the changes back: review %q, earlier %q, adopted from %v", label(out.Rev), ids(), rs.AdoptedFrom())
	}
	reload(disjoint)
	next := rs.Restore(rs.Find("pa"), rs.Of(repo, uncommitted), never)
	if next.Err != nil || label(next.Rev) != "current" || len(ids()) != 0 {
		t.Fatalf("restore: review %q, err %v, earlier %q", label(next.Rev), next.Err, ids())
	}
	if out := reload(disjoint); label(out.Rev) != "current" || out.Archived != nil || len(ids()) != 0 {
		t.Errorf("the restored review without its changes: review %q, archived %q, earlier %q", label(out.Rev), label(out.Archived), ids())
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
		open  func(rev *review.Review)
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
			storage := store.Store{Dir: t.TempDir()}
			for _, r := range []*review.Review{saved(archived, "earlier", "pb", nil), tc.saved} {
				if r == nil {
					continue
				}
				if err := storage.SaveReview(r); err != nil {
					t.Fatal(err)
				}
			}
			rs := New(storage)
			var open *review.Review
			if tc.open != nil {
				open = rs.Of(repo, commit)
				tc.open(open)
			}
			cur := rs.Of(repo, uncommitted)
			load(rs, uncommitted, cur, same).Decide(cur, func(*review.Review) bool { return false })
			busy := func(r *review.Review) bool { return tc.busy && r == open }

			next := rs.MoveTo(repo, rs.Find("pb"), commit, busy)
			if next.Err != nil {
				t.Fatal(next.Err)
			}
			if next.Left != tc.moved || next.Rev != nil {
				t.Fatalf("left = %v, review %q, want left %v and the review of the scope kept", next.Left, label(next.Rev), tc.moved)
			}
			if next.Open == nil || *next.Open != commit {
				t.Errorf("opens %v, want the commit", next.Open)
			}
			if got := label(storage.LoadReview(repo, commit)) == "earlier"; got != tc.moved {
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

// TestRestore brings an earlier review back to the empty review of the
// uncommitted changes, and refuses while that review is not empty or an
// agent writes into the earlier review.
func TestRestore(t *testing.T) {
	for _, tc := range []struct {
		name     string
		notEmpty bool
		busy     bool
		gone     bool
		restored bool
	}{
		{name: "restores into the empty review and opens it", restored: true},
		{name: "refuses while the uncommitted changes have a review", notEmpty: true},
		{name: "refuses while an agent writes into the earlier review", busy: true},
		{name: "fails when the earlier review is gone from the store", gone: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage := store.Store{Dir: t.TempDir()}
			if err := storage.SaveReview(saved(archived, "earlier", "pb", nil)); err != nil {
				t.Fatal(err)
			}
			rs := New(storage)
			open := rs.Of(repo, archived)
			cur := rs.Of(repo, uncommitted)
			load(rs, uncommitted, cur, same).Decide(cur, func(*review.Review) bool { return false })
			if tc.notEmpty {
				cur.Overview = &review.Overview{Markdown: "own"}
			}
			busy := func(r *review.Review) bool { return tc.busy && r == open }
			if tc.gone {
				removeReview(t, storage, archived)
			}

			next := rs.Restore(rs.Find("pb"), cur, busy)

			if next.Left != tc.restored || (next.Err != nil) == tc.restored {
				t.Fatalf("left = %v, err = %v, want restored %v", next.Left, next.Err, tc.restored)
			}
			if !tc.restored {
				if next.Rev != nil || next.Open != nil || rs.Find("pb") == nil || !storage.LoadReview(repo, uncommitted).Empty() ||
					!tc.gone && label(storage.LoadReview(repo, archived)) != "earlier" {
					t.Errorf("a refused restore changed something: %+v", next)
				}
				return
			}
			if label(next.Rev) != "earlier" || !next.Rev.Kept || rs.Find("pb") != nil {
				t.Errorf("review %q, kept %v, still listed %v", label(next.Rev), next.Rev.Kept, rs.Find("pb") != nil)
			}
			if next.Open == nil || *next.Open != uncommitted {
				t.Errorf("opens %v, want the uncommitted changes", next.Open)
			}
			if got := storage.LoadReview(repo, uncommitted); label(got) != "earlier" || !got.Kept {
				t.Errorf("saved review %q, kept %v", label(got), got.Kept)
			}
		})
	}
}

// TestDelete deletes an earlier review from the store and the list, and
// keeps it on the list when the store cannot delete it.
func TestDelete(t *testing.T) {
	for _, tc := range []struct {
		name    string
		broken  bool
		deleted bool
	}{
		{name: "deletes the earlier review", deleted: true},
		{name: "keeps the earlier review when the store fails", broken: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage := store.Store{Dir: t.TempDir()}
			if err := storage.SaveReview(saved(archived, "earlier", "pb", nil)); err != nil {
				t.Fatal(err)
			}
			rs := New(storage)
			cur := rs.Of(repo, uncommitted)
			load(rs, uncommitted, cur, same).Decide(cur, func(*review.Review) bool { return false })
			if tc.broken {
				// A file in place of the store directory makes every
				// delete fail.
				file := filepath.Join(t.TempDir(), "file")
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				rs.store = store.Store{Dir: file}
			}

			next := rs.Delete(repo, rs.Find("pb"))

			if next.Left != tc.deleted || (next.Err != nil) == tc.deleted || next.Open != nil || next.Rev != nil {
				t.Fatalf("left = %v, err = %v, open %v, want deleted %v", next.Left, next.Err, next.Open, tc.deleted)
			}
			if got := rs.Find("pb") == nil; got != tc.deleted {
				t.Errorf("left the list: %v", got)
			}
			if got := storage.LoadReview(repo, archived).Empty(); got != tc.deleted {
				t.Errorf("gone from the store: %v", got)
			}
		})
	}
}

// TestLocate finds the commit that holds the changes of an archived
// review, and a commit that changes all the files of another one.
func TestLocate(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	writeFile(t, dir, "a.go", "package a\n")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-qm", "first")
	writeFile(t, dir, "a.go", "package a\n\nvar A = 1\n")
	gitIn(t, dir, "commit", "-qam", "second")
	second := gitIn(t, dir, "rev-parse", "HEAD")
	gitRepo, err := gitx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := gitRepo.CommitPatchIDs(t.Context(), "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	patch := ""
	for p, sha := range ids {
		if sha == second {
			patch = p
		}
	}
	if patch == "" {
		t.Fatalf("no patch id for the second commit in %v", ids)
	}

	storage := store.Store{Dir: t.TempDir()}
	for _, rev := range []*review.Review{
		saved(gitx.ArchivedScope(patch), "moved", patch, map[string]string{"a.go": "x"}),
		saved(gitx.ArchivedScope("pz"), "edited", "pz", map[string]string{"a.go": "y"}),
	} {
		if err := storage.SaveReview(rev); err != nil {
			t.Fatal(err)
		}
	}
	rs := New(storage)
	cur := rs.Of(repo, uncommitted)
	uncommittedLoad := load(rs, uncommitted, cur, same)
	uncommittedLoad.Decide(cur, func(*review.Review) bool { return false })

	located := uncommittedLoad.Locate(t.Context(), gitRepo)
	if located == nil {
		t.Fatal("nothing to look for")
	}
	if movedTo := located(); movedTo != "" {
		t.Errorf("moved to %q for a review that did not drift", movedTo)
	}
	if earlier := rs.Find(patch); earlier == nil || earlier.MovedTo != second {
		t.Errorf("the review with the patch of the second commit: %+v, want moved to %s", earlier, second)
	}
	if earlier := rs.Find("pz"); earlier == nil || earlier.MovedTo != "" || earlier.Probably != second {
		t.Errorf("the review of other changes to a.go: %+v, want probably in %s", earlier, second)
	}

	commitLoad := load(rs, commit, rs.Of(repo, commit), same)
	if commitLoad.Locate(t.Context(), gitRepo) != nil {
		t.Error("a commit load without earlier reviews looks for commits")
	}
}

// TestRescope gives a review a scope that names the same changes, and
// refuses one that names other changes.
func TestRescope(t *testing.T) {
	long := "0123456789abcdef0123456789abcdef01234567"
	for _, tc := range []struct {
		name     string
		from, to gitx.Scope
		ok       bool
	}{
		{"a checked-out branch by its name", gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Head: "feat"}, gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Branch: "feat"}, true},
		{"a branch that is no longer checked out", gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Branch: "feat"}, gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Head: "feat"}, true},
		{"another branch", gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Branch: "feat"}, gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Head: "other"}, false},
		{"another base", gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Branch: "feat"}, gitx.Scope{Kind: gitx.ScopeBranch, Base: "dev", Branch: "feat"}, false},
		{"a commit by its full hash", gitx.Scope{Kind: gitx.ScopeCommit, Commit: long[:12]}, gitx.Scope{Kind: gitx.ScopeCommit, Commit: long}, true},
		{"another commit", gitx.Scope{Kind: gitx.ScopeCommit, Commit: long[:12]}, gitx.Scope{Kind: gitx.ScopeCommit, Commit: "fedcba9876543210"}, false},
		{"another kind of scope", uncommitted, gitx.Scope{Kind: gitx.ScopeCommit, Commit: long}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := New(store.Store{Dir: t.TempDir()})
			rev := rs.Of(repo, tc.from)
			if got := rs.Rescope(rev, tc.to); got != tc.ok {
				t.Fatalf("Rescope = %v, want %v", got, tc.ok)
			}
			want := tc.from
			if tc.ok {
				want = tc.to
			}
			if rev.Scope != want {
				t.Errorf("scope %+v, want %+v", rev.Scope, want)
			}
		})
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
