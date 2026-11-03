package app

import (
	"fmt"
	"slices"

	"github.com/psd-coder/agentdiff/internal/review"
)

// versionOn returns the overview version that the page shows or is
// under: the version of an Overview page, or the version above a walk.
// Other pages, and versions that are gone, give the latest.
func (app *app) versionOn(page page) *review.Overview {
	id := page.key
	if page.kind == kindWalk {
		id = app.rev.VersionOf(page.key)
	} else if page.kind != kindOverview {
		id = ""
	}
	if ov := app.rev.OverviewOf(id); ov != nil {
		return ov
	}
	return app.rev.Overview
}

// versionPage is the Overview page of a version: the plain Overview for
// the latest one, so that it follows a regenerate.
func (app *app) versionPage(ov *review.Overview) page {
	if ov == nil || ov == app.rev.Overview {
		return overview
	}
	return page{kindOverview, ov.ID}
}

// earlierVersion reports whether id is an earlier version of the overview.
func (app *app) earlierVersion(id string) bool {
	ov := app.rev.OverviewOf(id)
	return ov != nil && ov != app.rev.Overview
}

// targetVersion is the earlier version on screen, for the commands that
// act on one.
func (app *app) targetVersion() (string, bool) {
	ov := app.versionOn(app.nav.page)
	if ov == nil {
		return "", false
	}
	return ov.ID, app.nav.page.kind == kindOverview && app.earlierVersion(ov.ID)
}

// versionLabels names the versions in the switcher: the time, the prompt
// and the agent of each. A version written in the same minute as another
// gets the seconds too, and in the same second its number, so each label
// picks one version.
func versionLabels(versions []*review.Overview, latest *review.Overview) []string {
	label := func(ov *review.Overview, layout string) string {
		text := ov.Generated.Format(layout)
		switch ov.Prompt {
		case review.SourceRepo:
			text += " · repo prompt"
		case review.SourceSettings:
			text += " · settings prompt"
		case review.SourceDefault:
			text += " · default prompt"
		}
		text += " by " + ov.Agent.ShortName()
		if ov == latest {
			text += " (latest)"
		}
		return text
	}
	labels := make([]string, len(versions))
	count := map[string]int{}
	for i, ov := range versions {
		labels[i] = label(ov, "15:04 Jan 2")
		count[labels[i]]++
	}
	for i, ov := range versions {
		if count[labels[i]] > 1 {
			labels[i] = label(ov, "15:04:05 Jan 2")
		}
	}
	clear(count)
	for _, l := range labels {
		count[l]++
	}
	for i, ov := range versions {
		if count[labels[i]] > 1 {
			labels[i] += fmt.Sprintf(" (v%d)", ov.Version)
		}
	}
	return labels
}

// aheadOf returns the walks under an overview version that the app
// writes ahead, or that wait in the queue to be written.
func (app *app) aheadOf(version string) []string {
	var ids []string
	for _, target := range app.runner.aheadTargets(app.rev) {
		if app.rev.VersionOf(target) == version {
			ids = append(ids, target)
		}
	}
	return ids
}

// stopAhead stops the walks that the app writes ahead under a version,
// and deletes those without an answer, so a later Go deeper starts anew.
func (app *app) stopAhead(version string) {
	ids := app.aheadOf(version)
	app.runner.stop(app.rev, ids...)
	for _, id := range ids {
		if walk := app.rev.Walk(id); walk != nil && walk.Content == nil && walk.Markdown == "" {
			app.rev.DeleteWalk(id)
		}
	}
	app.closeGoneQuestion()
	app.leaveGone()
	app.saveReview()
}

// confirmDeleteVersion deletes an earlier version of the overview with
// the walks under it, after the user confirms. Threads stay.
func (app *app) confirmDeleteVersion(id string) {
	rev := app.rev
	ov := rev.OverviewOf(id)
	app.confirm(fmt.Sprintf("Delete overview v%d?", ov.Version), "Its deeper walkthroughs and the questions about it are deleted too. Comments stay.", "Delete", func() {
		if app.rev != rev || !app.earlierVersion(id) {
			return
		}
		var walks []string
		for _, w := range rev.Walks {
			if w.Parent == id {
				walks = append(walks, w.ID)
				walks = append(walks, rev.WalksUnder(w.ID)...)
			}
		}
		app.runner.stop(rev, walks...)
		rev.DeleteVersion(id)
		app.closeGoneQuestion()
		app.leaveGone()
		app.saveReview()
	})
}

// versionsAhead lists the earlier versions whose walks the app still
// writes ahead, oldest first.
func (app *app) versionsAhead() []*review.Overview {
	return slices.DeleteFunc(slices.Clone(app.rev.Previous), func(ov *review.Overview) bool { return len(app.aheadOf(ov.ID)) == 0 })
}
