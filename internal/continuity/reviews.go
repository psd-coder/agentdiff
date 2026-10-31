package continuity

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

// liveKey is the key of the review of the loaded scope among the
// changes a load looks for in the recent commits.
const liveKey = ""

// Reviews keeps the reviews the app has open, one per repository and
// scope, and the earlier reviews of the uncommitted changes. All its
// methods run on the UI thread, except Read.
type Reviews struct {
	store store.Store
	open  map[string]*review.Review
	// startedOver holds the keys of the scopes the user started over,
	// which take no saved review over until the app quits.
	startedOver map[string]bool
	// earlier are the archived reviews, the newest first.
	earlier []*Earlier
	// adoptedFrom is the scope the review was moved or copied from when
	// the scope opened.
	adoptedFrom *gitx.Scope
}

func New(s store.Store) *Reviews {
	return &Reviews{store: s, open: map[string]*review.Review{}, startedOver: map[string]bool{}}
}

func key(repo string, s gitx.Scope) string { return repo + "\x00" + s.Key() }

// Of loads the review of a scope once and keeps it, so an agent run that
// finishes after a scope switch writes into the review the view shows.
func (rs *Reviews) Of(repo string, s gitx.Scope) *review.Review {
	k := key(repo, s)
	r := rs.open[k]
	if r == nil {
		r = rs.store.LoadReview(repo, s)
		rs.open[k] = r
	}
	return r
}

// Kept returns the review of a scope that the app has open, or nil. It
// does not read the store.
func (rs *Reviews) Kept(repo string, s gitx.Scope) *review.Review { return rs.open[key(repo, s)] }

// Replace makes r the review of its scope.
func (rs *Reviews) Replace(r *review.Review) { rs.open[key(r.Repo, r.Scope)] = r }

// StartOver deletes the review of scope s of repo and starts an empty
// one. Until the app quits, a load takes no saved review over into s.
func (rs *Reviews) StartOver(repo string, s gitx.Scope) (*review.Review, error) {
	if err := rs.store.DeleteReview(repo, s); err != nil {
		return nil, err
	}
	r := review.New(repo, s)
	rs.Replace(r)
	rs.startedOver[key(repo, s)] = true
	return r, nil
}

// Obsolete reports whether r is no longer the review kept for its scope:
// it was started over, archived or replaced, and a late result must not
// write it back.
func (rs *Reviews) Obsolete(r *review.Review) bool { return rs.open[key(r.Repo, r.Scope)] != r }

// Earlier returns the archived reviews, the newest first.
func (rs *Reviews) Earlier() []*Earlier { return rs.earlier }

// Find returns the archived review with the id, or nil.
func (rs *Reviews) Find(id string) *Earlier {
	for _, e := range rs.earlier {
		if e.ID() == id {
			return e
		}
	}
	return nil
}

// Drop takes an archived review off the list.
func (rs *Reviews) Drop(e *Earlier) {
	rs.earlier = slices.DeleteFunc(rs.earlier, func(x *Earlier) bool { return x == e })
}

// AdoptedFrom is the scope the review of the open scope was moved or
// copied from, or nil.
func (rs *Reviews) AdoptedFrom() *gitx.Scope { return rs.adoptedFrom }

// ClearAdopted forgets where the review of the open scope came from,
// when another scope opens.
func (rs *Reviews) ClearAdopted() { rs.adoptedFrom = nil }

// Before is what the review of a scope was written for when its load
// started.
type Before struct {
	Empty   bool
	Written ChangeID
}

// BeforeLoad reads rev for a load, on the UI thread.
func BeforeLoad(rev *review.Review) Before {
	b := Before{Empty: rev.Empty()}
	if ov := rev.Overview; ov != nil {
		b.Written = changeOf(ov)
	}
	return b
}

func changeOf(ov *review.Overview) ChangeID {
	return ChangeID{Patch: ov.PatchID, Files: ov.WrittenFiles()}
}

// Found is what a load read from the store for its scope.
type Found struct {
	fp      gitx.Fingerprint
	fpKnown bool
	from    *review.Review
	earlier []*Earlier
	// live holds the commits found for the review of the scope, and goes
	// to the list when the review goes to the archive.
	live   *Earlier
	wanted map[string]ChangeID
}

// Read reads what the store has for a load of s, whose changes have the
// fingerprint fp unless fpErr is set: the archived reviews of the
// uncommitted changes, and a saved review written for the same changes
// when the review of s is empty or a drifted review of uncommitted
// changes. It runs on the load goroutine, so it reads the review of s as
// it was when the load started, and Decide checks the review again.
func (rs *Reviews) Read(repo string, s gitx.Scope, b Before, fp gitx.Fingerprint, fpErr error) *Found {
	uncommitted := s.Kind == gitx.ScopeUncommitted
	f := &Found{fp: fp, fpKnown: fpErr == nil, live: &Earlier{}, wanted: map[string]ChangeID{}}
	if uncommitted {
		for _, arch := range rs.store.Archived(repo) {
			f.earlier = append(f.earlier, &Earlier{Rev: arch})
		}
	}
	written := b.Written.Patch
	if uncommitted && f.fpKnown && written != "" && written != fp.Patch {
		f.wanted[liveKey] = b.Written
	}
	for _, e := range f.earlier {
		f.wanted[e.ID()] = changeOf(e.Rev.Overview)
	}
	if (b.Empty || uncommitted && written != fp.Patch) && f.fpKnown && fp.Patch != "" {
		if src, ok := rs.store.FindReview(repo, s, fp.Patch); ok {
			f.from = src
		}
	}
	return f
}

// Searches reports whether the load looks for changes in the recent
// commits.
func (f *Found) Searches() bool { return len(f.wanted) > 0 }

// FindCommits looks in the recent commits for the changes of the
// archived reviews and of a drifted review of the scope, on the load
// goroutine.
func (f *Found) FindCommits(ctx context.Context, r *gitx.Repo) (moved, probable map[string]string) {
	return findCommits(ctx, r, f.wanted)
}

// Located puts the commits FindCommits found into the archived reviews,
// on the UI thread. It returns the commit that now holds the changes of
// the review of the scope, unless that review went to the archive.
func (rs *Reviews) Located(f *Found, moved, probable map[string]string) string {
	f.live.MovedTo, f.live.Probably = moved[liveKey], probable[liveKey]
	for _, e := range f.earlier {
		e.MovedTo, e.Probably = moved[e.ID()], probable[e.ID()]
	}
	if slices.Contains(rs.earlier, f.live) {
		return ""
	}
	return f.live.MovedTo
}

// Outcome is what a decision did to the review of the scope.
type Outcome struct {
	// Rev is the review of the scope now.
	Rev *review.Review
	// Archived is the review that went to the archive, or nil.
	Archived *review.Review
	// Dropped are the reviews whose runs the app releases.
	Dropped []*review.Review
	Err     error
}

// Decide settles the review of the scope after a load, on the UI thread:
// it takes over the saved review written for the same changes, and moves
// the review of the uncommitted changes to the archive when none of its
// files is changed any more. rev is the review of the scope, and busy
// reports whether an agent run writes into a review. While an agent
// writes into rev or into the saved review, Decide changes nothing.
func (rs *Reviews) Decide(f *Found, rev *review.Review, busy func(*review.Review) bool) Outcome {
	out := Outcome{Rev: rev}
	// The list gets its own array: deleting from it in place would clear
	// entries the commit lookup still reads.
	rs.earlier = slices.Clone(f.earlier)
	out.Err = rs.decide(&out, f, busy)
	if from := rs.adoptedFrom; from != nil {
		rs.earlier = slices.DeleteFunc(rs.earlier, func(e *Earlier) bool { return e.Rev.Scope == *from })
	}
	return out
}

func (rs *Reviews) decide(out *Outcome, f *Found, busy func(*review.Review) bool) error {
	rev := out.Rev
	if !f.fpKnown || busy(rev) {
		return nil
	}
	src := f.from
	if rs.startedOver[key(rev.Repo, rev.Scope)] {
		src = nil
	}
	if src != nil && rs.busy(rev.Repo, src.Scope, busy) {
		return nil
	}
	switch {
	case src != nil && rev.Empty():
		return rs.adopt(out, src)
	case !archivable(rev, f.fp):
		return nil
	case src != nil:
		return rs.archiveAndAdopt(out, f, src)
	case rev.Overview.Drift(f.fp.Files).Disjoint():
		return rs.archive(out, f)
	}
	return nil
}

// busy reports whether an agent writes into the open review of scope s.
func (rs *Reviews) busy(repo string, s gitx.Scope, busy func(*review.Review) bool) bool {
	open := rs.open[key(repo, s)]
	return open != nil && busy(open)
}

// archivable reports whether rev, the review of the uncommitted changes,
// can go to the archive: it has an overview written for other changes
// than fp, and the user did not restore it.
func archivable(rev *review.Review, fp gitx.Fingerprint) bool {
	ov := rev.Overview
	return rev.Scope.Kind == gitx.ScopeUncommitted && ov.Done() && ov.PatchID != fp.Patch && !rev.Kept
}

// adopt takes over the saved review src, which was written for the same
// changes as the scope of out.Rev has.
func (rs *Reviews) adopt(out *Outcome, src *review.Review) error {
	moved, err := rs.take(out.Rev.Repo, src, out.Rev.Scope)
	if err != nil {
		return err
	}
	return rs.took(out, src, moved)
}

// take moves or copies the saved review src to scope to in the store, and
// reports whether it moved. A review of uncommitted changes moves,
// because those changes are gone from the working tree; any other one is
// copied.
func (rs *Reviews) take(repo string, src *review.Review, to gitx.Scope) (bool, error) {
	move := src.Scope.Kind == gitx.ScopeUncommitted || src.Scope.Kind == gitx.ScopeArchived
	var err error
	if move {
		err = rs.store.MoveReview(repo, src.Scope, to)
	} else {
		err = rs.store.CopyReview(repo, src.Scope, to)
	}
	if err != nil {
		return move, fmt.Errorf("adopt review: %w", err)
	}
	return move, nil
}

// took makes src, which take put into the scope of out.Rev in the store,
// the review of that scope. A file stays viewed when out.Rev marked it.
func (rs *Reviews) took(out *Outcome, src *review.Review, moved bool) error {
	cur := out.Rev
	repo, srcScope := cur.Repo, src.Scope
	srcKey := key(repo, srcScope)
	from := rs.open[srcKey]
	if moved && from != nil {
		delete(rs.open, srcKey)
		out.Dropped = append(out.Dropped, from)
	}
	out.Dropped = append(out.Dropped, cur)
	if from != nil {
		// A run on the open review can save after src was read.
		out.Rev = rs.store.LoadReview(repo, cur.Scope)
	} else {
		rev := *src
		rev.Repo, rev.Scope = repo, cur.Scope
		rev.Viewed = maps.Clone(src.Viewed)
		out.Rev = &rev
	}
	rs.Replace(out.Rev)
	rs.adoptedFrom = &srcScope
	return rs.addViewed(out.Rev, cur.Viewed)
}

// addViewed marks the files viewed in r as well, and saves r when that
// adds a mark.
func (rs *Reviews) addViewed(r *review.Review, viewed map[string]bool) error {
	added := false
	for path, v := range viewed {
		if v && !r.Viewed[path] {
			if r.Viewed == nil {
				r.Viewed = map[string]bool{}
			}
			r.Viewed[path] = true
			added = true
		}
	}
	if !added {
		return nil
	}
	if err := rs.store.SaveReview(r); err != nil {
		return fmt.Errorf("save viewed files: %w", err)
	}
	return nil
}

// archive moves out.Rev, the review of the uncommitted changes, into the
// archive and starts an empty one.
func (rs *Reviews) archive(out *Outcome, f *Found) error {
	old := out.Rev
	to, err := rs.store.ArchiveReview(old.Repo, archiveID(old.Overview))
	if err != nil {
		return fmt.Errorf("archive review: %w", err)
	}
	rs.archived(out, f, to)
	return rs.prune(f, old.Repo)
}

// archiveAndAdopt moves out.Rev, the review of the uncommitted changes,
// into the archive and takes over the saved review src in its place.
// When src cannot be taken over, the review comes back from the archive.
func (rs *Reviews) archiveAndAdopt(out *Outcome, f *Found, src *review.Review) error {
	old := out.Rev
	if rs.store.LoadReview(old.Repo, src.Scope).Empty() {
		return fmt.Errorf("adopt review: the review of %s is gone", src.Scope.Key())
	}
	to, err := rs.store.ArchiveReview(old.Repo, archiveID(old.Overview))
	if err != nil {
		return fmt.Errorf("archive review: %w", err)
	}
	moved, err := rs.take(old.Repo, src, old.Scope)
	if err != nil {
		return errors.Join(err, rs.store.MoveReview(old.Repo, to, old.Scope))
	}
	rs.archived(out, f, to)
	err = rs.took(out, src, moved)
	return errors.Join(err, rs.prune(f, old.Repo))
}

// archived puts out.Rev, which the store archived under scope to, on the
// list and starts an empty review. f.live holds the commits found for the
// archived review's changes.
func (rs *Reviews) archived(out *Outcome, f *Found, to gitx.Scope) {
	old := out.Rev
	scope := old.Scope
	old.Scope = to
	f.live.Rev = old
	rs.adoptedFrom = nil
	out.Rev = review.New(old.Repo, scope)
	rs.Replace(out.Rev)
	out.Archived = old
	out.Dropped = append(out.Dropped, old)
}

// prune deletes the oldest archived reviews of repo past what the store
// keeps, and reads the list again from the store. The list keeps the
// entries of f, which receive the commits the load finds.
func (rs *Reviews) prune(f *Found, repo string) error {
	err := rs.store.PruneArchive(repo)
	if err != nil {
		err = fmt.Errorf("prune archive: %w", err)
	}
	listed := map[string]*Earlier{}
	for _, e := range f.earlier {
		listed[e.ID()] = e
	}
	if f.live.Rev != nil {
		listed[f.live.ID()] = f.live
	}
	rs.earlier = nil
	for _, r := range rs.store.Archived(repo) {
		e := listed[r.Scope.From]
		if e == nil {
			e = &Earlier{Rev: r}
		}
		rs.earlier = append(rs.earlier, e)
	}
	return err
}

// Next is what an action on an earlier review did, and what the window
// shows next.
type Next struct {
	// Outcome is set when the action replaced the review of the scope:
	// Rev is the new review. Err is why the action failed or did nothing.
	Outcome
	// Left reports that the earlier review left the list, so its page
	// closes.
	Left bool
	// Open is the scope whose review the window shows next, or nil.
	Open *gitx.Scope
}

// Restore brings the archived review e back to the empty review rev of
// the uncommitted changes, and marks it kept, so it does not go back to
// the archive by itself. It does nothing while rev is not empty or an
// agent writes into e, and Err says why.
func (rs *Reviews) Restore(e *Earlier, rev *review.Review, busy func(*review.Review) bool) Next {
	if !rev.Empty() {
		return Next{Err: errors.New("the uncommitted changes have a review of their own")}
	}
	if rs.busy(rev.Repo, e.Rev.Scope, busy) {
		return Next{Err: errors.New("an agent still writes into the earlier review")}
	}
	out := Outcome{Rev: rev}
	if out.Err = rs.adopt(&out, e.Rev); out.Rev == rev {
		return Next{Err: out.Err}
	}
	rs.adoptedFrom = nil
	rs.Drop(e)
	out.Rev.Kept = true
	if err := rs.store.SaveReview(out.Rev); err != nil {
		out.Err = errors.Join(out.Err, fmt.Errorf("keep review: %w", err))
	}
	scope := rev.Scope
	return Next{Outcome: out, Left: true, Open: &scope}
}

// MoveTo moves the archived review e to scope to of repo, when to has no
// review of its own, neither saved nor open, and opens to.
func (rs *Reviews) MoveTo(repo string, e *Earlier, to gitx.Scope, busy func(*review.Review) bool) Next {
	k := key(repo, to)
	if open := rs.open[k]; open != nil && (!open.Empty() || busy(open)) {
		return Next{Open: &to}
	}
	if !rs.store.LoadReview(repo, to).Empty() {
		return Next{Open: &to}
	}
	if err := rs.store.MoveReview(repo, e.Rev.Scope, to); err != nil {
		return Next{Err: err}
	}
	delete(rs.open, k)
	rs.Drop(e)
	return Next{Left: true, Open: &to}
}

// Delete deletes the archived review e.
func (rs *Reviews) Delete(repo string, e *Earlier) Next {
	if err := rs.store.DeleteReview(repo, e.Rev.Scope); err != nil {
		return Next{Err: err}
	}
	rs.Drop(e)
	return Next{Left: true}
}
