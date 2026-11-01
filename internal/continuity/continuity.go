// Package continuity lets a review follow its changes: earlier reviews,
// the commit that now holds their changes, and the archive id.
package continuity

import (
	"cmp"
	"context"
	"fmt"
	"log"

	"github.com/psd-coder/agentdiff/internal/gitx"
	"github.com/psd-coder/agentdiff/internal/review"
)

// commitsSearched is how many recent commits hold the changes of an
// earlier review that moved.
const commitsSearched = 50

// Earlier is an archived review, with the commit that now holds the
// changes it was written for. When none does, Probably is a commit that
// changes all of its files.
type Earlier struct {
	Rev      *review.Review
	MovedTo  string
	Probably string
}

// ID names the archived review in the page that shows it.
func (earlier *Earlier) ID() string { return earlier.Rev.Scope.From }

// archiveID names an archived review: the patch ID of its overview, or
// the time it was written for an overview from before patch IDs.
func archiveID(ov *review.Overview) string {
	return cmp.Or(ov.PatchID, fmt.Sprint(ov.Generated.Unix()))
}

// ChangeID identifies the changes a review was written for.
type ChangeID struct {
	Patch string
	Files []string
}

// findCommits looks for each wanted change in the recent commits, by
// key. moved maps a key to the commit with the same changes. When no
// commit has the same changes, probable maps the key to a commit that
// changes all of its files.
func findCommits(ctx context.Context, gitRepo *gitx.Repo, wanted map[string]ChangeID) (moved, probable map[string]string) {
	ids, err := gitRepo.CommitPatchIDs(ctx, "", "", commitsSearched)
	if err != nil {
		log.Printf("commit patch ids: %v", err)
	}
	moved, sets := map[string]string{}, map[string][]string{}
	for key, w := range wanted {
		if sha := ids[w.Patch]; sha != "" && w.Patch != "" {
			moved[key] = sha
		} else {
			sets[key] = w.Files
		}
	}
	probable, err = gitRepo.CommitsWithFiles(ctx, sets, commitsSearched)
	if err != nil {
		log.Printf("commits with files: %v", err)
	}
	return moved, probable
}
