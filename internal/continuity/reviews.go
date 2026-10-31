package continuity

import (
	"context"
	"fmt"
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
	// kept holds the archive IDs of reviews restored in this session,
	// which stay even though none of their files is changed.
	kept map[string]bool
	// earlier are the archived reviews, the newest first.
	earlier []*Earlier
	// adoptedFrom is the scope the review was moved or copied from when
	// the scope opened.
	adoptedFrom *gitx.Scope
}

func New(s store.Store) *Reviews {
	return &Reviews{store: s, open: map[string]*review.Review{}, kept: map[string]bool{}}
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

// Replace makes r the review of its scope.
func (rs *Reviews) Replace(r *review.Review) { rs.open[key(r.Repo, r.Scope)] = r }

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
// changes. It runs on the load goroutine.
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
// reports whether an agent run writes into a review.
func (rs *Reviews) Decide(f *Found, rev *review.Review, busy func(*review.Review) bool) Outcome {
	out := Outcome{Rev: rev}
	// The list gets its own array: deleting from it in place would clear
	// entries the commit lookup still reads.
	rs.earlier = slices.Clone(f.earlier)
	archivable := rs.archivable(rev, f.fpKnown, busy)
	switch {
	case f.from != nil && rev.Empty():
		out.Err = rs.adopt(&out, f.from, busy)
	case f.from != nil && archivable:
		if out.Err = rs.archive(&out, f.live); out.Err == nil {
			out.Err = rs.adopt(&out, f.from, busy)
		}
	case archivable && rev.Overview.Drift(f.fp.Files).Disjoint():
		out.Err = rs.archive(&out, f.live)
	}
	if from := rs.adoptedFrom; from != nil {
		rs.earlier = slices.DeleteFunc(rs.earlier, func(e *Earlier) bool { return e.Rev.Scope == *from })
	}
	return out
}

// archivable reports whether rev, the review of the uncommitted changes,
// can go to the archive: it has an overview, no agent writes into it,
// and the user did not restore it in this session.
func (rs *Reviews) archivable(rev *review.Review, fpKnown bool, busy func(*review.Review) bool) bool {
	ov := rev.Overview
	return rev.Scope.Kind == gitx.ScopeUncommitted && ov.Done() && fpKnown && !rs.kept[archiveID(ov)] && !busy(rev)
}

// adopt takes over the saved review src, which was written for the same
// changes as the scope of out.Rev has. A review of uncommitted changes
// moves, because those changes are gone from the working tree; any other
// one is copied.
func (rs *Reviews) adopt(out *Outcome, src *review.Review, busy func(*review.Review) bool) error {
	cur := out.Rev
	if !cur.Empty() {
		return nil
	}
	repo, srcScope := cur.Repo, src.Scope
	srcKey := key(repo, srcScope)
	from := rs.open[srcKey]
	if from != nil && busy(from) {
		return nil
	}
	move := srcScope.Kind == gitx.ScopeUncommitted || srcScope.Kind == gitx.ScopeArchived
	var err error
	if move {
		err = rs.store.MoveReview(repo, srcScope, cur.Scope)
	} else {
		err = rs.store.CopyReview(repo, srcScope, cur.Scope)
	}
	if err != nil {
		return fmt.Errorf("adopt review: %w", err)
	}
	if move && from != nil {
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
		out.Rev = &rev
	}
	rs.Replace(out.Rev)
	rs.adoptedFrom = &srcScope
	return nil
}

// archive moves out.Rev, the review of the uncommitted changes, into the
// archive and starts an empty one. e holds the commits found for the
// review's changes.
func (rs *Reviews) archive(out *Outcome, e *Earlier) error {
	old := out.Rev
	to, err := rs.store.ArchiveReview(old.Repo, archiveID(old.Overview))
	if err != nil {
		return fmt.Errorf("archive review: %w", err)
	}
	scope := old.Scope
	old.Scope = to
	e.Rev = old
	rs.earlier = append([]*Earlier{e}, rs.earlier...)
	rs.adoptedFrom = nil
	out.Rev = review.New(old.Repo, scope)
	rs.Replace(out.Rev)
	out.Archived = old
	out.Dropped = append(out.Dropped, old)
	return nil
}

// Restore brings the archived review e back to the empty review rev of
// the uncommitted changes, which keeps it from then on.
func (rs *Reviews) Restore(e *Earlier, rev *review.Review, busy func(*review.Review) bool) Outcome {
	out := Outcome{Rev: rev}
	if out.Err = rs.adopt(&out, e.Rev, busy); out.Err != nil {
		return out
	}
	rs.kept[archiveID(e.Rev.Overview)] = true
	rs.adoptedFrom = nil
	rs.Drop(e)
	return out
}

// MoveTo moves the archived review e to scope to of repo, when to has no
// review of its own. It reports whether e moved.
func (rs *Reviews) MoveTo(repo string, e *Earlier, to gitx.Scope) (bool, error) {
	if !rs.store.LoadReview(repo, to).Empty() {
		return false, nil
	}
	if err := rs.store.MoveReview(repo, e.Rev.Scope, to); err != nil {
		return false, err
	}
	delete(rs.open, key(repo, to))
	rs.Drop(e)
	return true, nil
}

// Delete deletes the archived review e.
func (rs *Reviews) Delete(repo string, e *Earlier) error {
	if err := rs.store.DeleteReview(repo, e.Rev.Scope); err != nil {
		return err
	}
	rs.Drop(e)
	return nil
}
