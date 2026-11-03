package review

import (
	"encoding/json"
	"slices"
	"strconv"
)

// legacyOverviewID is the ID of an overview saved before overviews had
// IDs. A review held one overview then, so one fixed ID is enough.
const legacyOverviewID = "first"

// legacyStepID is the ID of a step saved before steps had IDs, made from
// its place, which the walks into it saved.
func legacyStepID(index int) string { return "s" + strconv.Itoa(index+1) }

// Versions returns the versions of the overview, oldest first, the
// latest last.
func (review *Review) Versions() []*Overview {
	if review.Overview == nil {
		return slices.Clone(review.Previous)
	}
	return append(slices.Clone(review.Previous), review.Overview)
}

// OverviewOf returns the version of the overview with the ID.
func (review *Review) OverviewOf(id string) *Overview {
	if id == "" {
		return nil
	}
	for _, ov := range review.Versions() {
		if ov.ID == id {
			return ov
		}
	}
	return nil
}

// VersionOf returns the ID of the overview version that a walk is under.
func (review *Review) VersionOf(walkID string) string {
	path := review.WalkPath(walkID)
	if len(path) == 0 {
		return ""
	}
	return path[0].Parent
}

// DeleteVersion deletes an earlier version of the overview with every
// walk under it. The latest version stays: Start over deletes it.
func (review *Review) DeleteVersion(id string) {
	index := slices.IndexFunc(review.Previous, func(ov *Overview) bool { return ov.ID == id })
	if index < 0 {
		return
	}
	for _, w := range slices.Clone(review.Walks) {
		if w.Parent == id {
			review.DeleteWalk(w.ID)
		}
	}
	review.Previous = slices.Delete(review.Previous, index, index+1)
}

// UnmarshalJSON reads a review. The walks of a review saved before
// overviews had versions point at the overview with an empty parent, so
// they get the ID of the overview.
func (review *Review) UnmarshalJSON(data []byte) error {
	type plain Review
	if err := json.Unmarshal(data, (*plain)(review)); err != nil {
		return err
	}
	if review.Overview == nil {
		return nil
	}
	for _, w := range review.Walks {
		if w.Parent == "" {
			w.Parent = review.Overview.ID
		}
	}
	return nil
}
