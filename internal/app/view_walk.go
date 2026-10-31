package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/review"
)

// extraFile is a file a walkthrough points into that the changes leave
// alone, read when a snippet first needs it.
type extraFile struct {
	lines   []string
	err     string
	loading bool
}

// reveal is a hunk to scroll to once the file's diff is on screen.
type reveal struct {
	file string
	hunk int
}

// walkthrough shows a summary and its steps. parent is the walk the steps
// belong to, empty for the overview.
func (a *app) walkthrough(c *ui.Context, parent string, w *review.Walkthrough) {
	if w.Summary != "" {
		ui.Text(c, w.Summary).FontSize(15).Selectable()
	}
	for i, s := range w.Steps {
		a.stepCard(c.Key(fmt.Sprintf("step-%s-%d", parent, i)), parent, i, s)
	}
}

func (a *app) stepCard(c *ui.Context, parent string, i int, s review.Step) {
	t := c.Theme()
	ui.Column(c).Gap(10).Padding(16, 20).Radius(10).Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("%d. %s", i+1, s.Title)).FontSize(16).Bold().Grow(1)
			a.sectionThreads(c, a.stepLabel(parent, s), s.Text)
		})
		if s.Text != "" {
			markdown.View(c, s.Text, &a.md)
		}
		for ri, r := range s.Refs {
			a.refView(c.Key(fmt.Sprintf("ref-%d", ri)), r)
		}
		var deeper *review.Walk
		var asked []*review.Walk
		for _, w := range a.rev.WalksOn(parent, i) {
			if w.Question == "" && deeper == nil {
				deeper = w
			} else {
				asked = append(asked, w)
			}
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if deeper != nil {
				ui.Button(c, "Deeper ›").Tooltip("Open the deeper walkthrough of this step").OnClick(func() { a.nav.goTo(page{kindWalk, deeper.ID}) })
				if a.busy(deeper.ID) {
					ui.Spinner(c).Size(12, 12)
				}
			} else {
				ui.PrimaryButton(c, "Go deeper").Tooltip("A walkthrough of this step, one level closer to the code").OnClick(func() { a.deeper(parent, i, "") })
			}
			ui.Button(c, "Ask about this step").Tooltip("Ask in the side panel").OnClick(func() { a.askAbout(parent, i) })
		})
		for _, w := range asked {
			a.walkRow(c, w)
		}
	})
}

// walkRow links to a walk from the page above it.
func (a *app) walkRow(c *ui.Context, w *review.Walk) {
	t := c.Theme()
	row := ui.ButtonBase(c.Key("walk-"+w.ID)).Padding(8, 12).Gap(8).Radius(8).Border(1, t.Border).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Text(c, "›").TextColor(t.Accent)
		ui.Text(c, a.rev.WalkTitle(w)).SingleLine().Shrink(1).Grow(1)
		targetMark(c, a.state(w.ID))
	})
	if row.Clicked() {
		if w.Question != "" {
			a.openQuestion(w.ID)
		} else {
			a.nav.goTo(page{kindWalk, w.ID})
		}
	}
}

// stepLabel names a step for the threads about it: the titles of the
// walks above it and its own.
func (a *app) stepLabel(parent string, s review.Step) string {
	var parts []string
	for _, w := range a.rev.WalkPath(parent) {
		parts = append(parts, a.rev.WalkTitle(w))
	}
	return strings.Join(append(parts, s.Title), " › ")
}

func (a *app) checklist(c *ui.Context, items []review.CheckItem) {
	t := c.Theme()
	if len(items) == 0 {
		return
	}
	ui.Text(c, "Checklist").FontSize(18).Bold().Margin(8, 0, 0, 0)
	for i, it := range items {
		ui.Column(c.Key(fmt.Sprintf("check-%d", i))).Gap(6).Padding(12, 16).Radius(10).Border(1, t.Border).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, it.Question).Bold().Grow(1)
				a.sectionThreads(c, "Checklist › "+it.Question, it.Answer)
			})
			markdown.View(c, it.Answer, &a.md)
			for ri, r := range it.Refs {
				a.refView(c.Key(fmt.Sprintf("ref-%d", ri)), r)
			}
		})
	}
}

// snippet is the lines a reference shows, with the ones the change
// touched marked.
type snippet struct {
	lines   []string
	changed []bool
	inDiff  bool
	loading bool
	missing string
}

func (a *app) snippetOf(r review.Ref) snippet {
	if a.diffs[r.File] == nil {
		a.extraOf(r.File)
	}
	s, _ := a.loadedSnippet(r)
	return s
}

// loadedSnippet is the snippet of a reference from the files read so
// far. It reports false when its file is not read yet.
func (a *app) loadedSnippet(r review.Ref) (snippet, bool) {
	if d := a.diffs[r.File]; d != nil {
		if d.state == nil {
			return snippet{loading: d.loading, missing: d.err}, true
		}
		buf := d.state.New
		if r.Side == review.SideOld {
			buf = d.state.Old
		}
		n := buf.LineCount()
		if n > 0 && buf.Line(n-1) == "" {
			n--
		}
		s := cut(n, buf.Line, r)
		s.inDiff = true
		if s.missing == "" {
			hunks := d.state.Hunks()
			for i := range s.lines {
				s.changed[i] = inHunk(hunks, r.Side, r.Start-1+i)
			}
		}
		return s, true
	}
	f := a.extra[r.File]
	if f == nil {
		return snippet{}, false
	}
	if f.loading || f.err != "" {
		return snippet{loading: f.loading, missing: f.err}, true
	}
	return cut(len(f.lines), func(i int) string { return f.lines[i] }, r), true
}

// savedSnippet is the snippet of a reference from the code the review
// saved, for a review whose changes are gone.
func savedSnippet(r review.Ref) snippet {
	if len(r.Code) == 0 {
		return snippet{missing: "The code of this snippet was not saved with the review."}
	}
	var s snippet
	for _, l := range r.Code {
		s.lines = append(s.lines, l.Text)
		s.changed = append(s.changed, l.Changed)
	}
	return s
}

func cut(count int, line func(int) string, r review.Ref) snippet {
	if r.Start > count {
		return snippet{missing: fmt.Sprintf("The file has %d lines", count)}
	}
	var s snippet
	for i := r.Start - 1; i < min(r.End, count); i++ {
		s.lines = append(s.lines, line(i))
	}
	s.changed = make([]bool, len(s.lines))
	return s
}

func inHunk(hunks []editor.DiffHunk, side review.Side, line int) bool {
	for _, h := range hunks {
		lo, hi := h.NewStart, h.NewEnd
		if side == review.SideOld {
			lo, hi = h.OldStart, h.OldEnd
		}
		if line >= lo && line < hi {
			return true
		}
	}
	return false
}

// extraOf returns a file outside the changes, reading it on first use.
func (a *app) extraOf(path string) *extraFile {
	if f := a.extra[path]; f != nil {
		return f
	}
	// The first load has no snapshot yet. The view asks again once it shows.
	if a.snap == nil {
		return &extraFile{loading: true}
	}
	f := &extraFile{loading: true}
	a.extra[path] = f
	snap, gen := a.snap, a.gen
	a.async(func() {
		text, err := snap.File(context.Background(), path)
		a.update(func() {
			if gen != a.gen {
				return
			}
			*f = *newExtraFile(text, err)
			if a.keepSnippets() {
				a.saveReview()
			}
		})
	})
	return f
}

// newExtraFile is a file outside the changes, read as text, or failing
// with err.
func newExtraFile(text string, err error) *extraFile {
	if err != nil {
		return &extraFile{err: err.Error()}
	}
	return &extraFile{lines: strings.Split(strings.TrimSuffix(text, "\n"), "\n")}
}

func (a *app) refView(c *ui.Context, r review.Ref) {
	a.snippetView(c, r, a.snippetOf(r))
}

// snippetView shows the lines of a reference under its label. The label
// opens the diff when the lines are in it.
func (a *app) snippetView(c *ui.Context, r review.Ref, s snippet) {
	t := c.Theme()
	ui.Column(c).Radius(8).Border(1, t.Border).Clip().Children(func() {
		ui.Row(c).Gap(10).Padding(6, 10).AlignItems(ui.Center).Background(t.Surface).Children(func() {
			if s.inDiff {
				if textLink(c, r.Label(), func(e ui.Element) { e.Font("monospace").FontSize(12) }).Shrink(0).Tooltip("Open in the diff").Clicked() {
					a.openRef(r)
				}
			} else {
				ui.Text(c, r.Label()).Font("monospace").FontSize(12).SingleLine().Shrink(0)
			}
			if r.Why != "" {
				ui.Text(c, r.Why).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1)
			}
		})
		switch {
		case s.loading:
			ui.Spinner(c).Size(12, 12).Margin(8, 10)
		case s.missing != "":
			ui.Text(c, s.missing).FontSize(12).TextColor(t.TextMuted).Padding(6, 10)
		}
		mark := t.Success.Alpha(0.14)
		if r.Side == review.SideOld {
			mark = t.Danger.Alpha(0.14)
		}
		for i, line := range s.lines {
			row := ui.Row(c.Key(i)).Gap(10).Padding(1, 10)
			if s.changed[i] {
				row.Background(mark)
			}
			row.Children(func() {
				ui.Text(c, fmt.Sprint(r.Start+i)).Font("monospace").FontSize(12).TextColor(t.TextMuted).Width(32).TextAlign(ui.End)
				ui.Text(c, strings.ReplaceAll(line, "\t", "    ")).Font("monospace").FontSize(12).SingleLine().Shrink(1)
			})
		}
	})
}

// openRef shows a reference in its file's diff, with its lines selected.
func (a *app) openRef(r review.Ref) {
	d := a.diffs[r.File]
	if d == nil || d.state == nil {
		return
	}
	a.nav.goTo(page{kindFile, r.File})
	d.state.SetSelectedLines(editor.DiffLineRange{Side: diffSide(r.Side), Start: r.Start - 1, End: r.End - 1}, true)
	a.reveal = &reveal{file: r.File, hunk: nearestHunk(d.state.Hunks(), r)}
}

// nearestHunk is the hunk holding the reference's first line, or the
// closest one to it.
func nearestHunk(hunks []editor.DiffHunk, r review.Ref) int {
	best, dist := -1, 0
	line := r.Start - 1
	for i, h := range hunks {
		lo, hi := h.NewStart, h.NewEnd
		if r.Side == review.SideOld {
			lo, hi = h.OldStart, h.OldEnd
		}
		d := 0
		switch {
		case line < lo:
			d = lo - line
		case line >= hi:
			d = line - hi + 1
		}
		if best < 0 || d < dist {
			best, dist = i, d
		}
	}
	return best
}

func (a *app) walkPage(c *ui.Context, id string) {
	t := c.Theme()
	w := a.rev.Walk(id)
	if w == nil {
		a.overviewPage(c)
		return
	}
	run := a.runner.run(a.rev, w.ID)
	st := a.state(w.ID)
	path := a.rev.WalkPath(w.ID)
	a.pageScroll(c, page{kindWalk, w.ID}).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Padding(24, 32).Gap(16).MaxWidth(980).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
				crumb := func(label string, p page) {
					link := textLink(c, excerpt(label, 40), func(e ui.Element) { e.FontSize(12) }).Tooltip(label)
					if link.Clicked() {
						a.nav.goTo(p)
					}
					ui.Text(c, "›").FontSize(12).TextColor(t.TextMuted)
				}
				crumb("Overview", overview)
				for _, p := range path[:len(path)-1] {
					crumb(a.rev.WalkTitle(p), page{kindWalk, p.ID})
				}
				ui.Text(c, excerpt(a.rev.WalkTitle(w), 40)).FontSize(12).TextColor(t.TextMuted)
			})
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.rev.WalkTitle(w)).FontSize(22).Bold().Grow(1).Shrink(1)
				a.cmdButton(c, "stop-walk", w.ID, "Stop")
				a.cmdButton(c, "regenerate-walk", w.ID, "Regenerate")
				a.cmdButton(c, "walk-log", w.ID, "Log")
				a.cmdButton(c, "delete-walk", w.ID, "Delete")
			})
			if s, ok := a.rev.StepOf(w); ok && w.Question != "" {
				ui.Text(c, "About the step: "+s.Title).FontSize(12).TextColor(t.TextMuted)
			}
			a.staleBanner(c)
			switch {
			case run != nil && len(w.More) == 0:
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, run.activity).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1)
				})
			case st.status == targetQueued:
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, fmt.Sprintf("Waiting: the app writes %d walkthroughs at a time.", maxAhead)).FontSize(12).TextColor(t.TextMuted)
					ui.Button(c, "Start now").OnClick(func() { a.runner.startNow(a.rev, w.ID) })
				})
			case w.Content != nil:
				a.walkthrough(c, w.ID, w.Content)
			case w.Markdown != "":
				markdown.View(c, w.Markdown, &a.md)
			}
			for i, turn := range w.More {
				ui.Column(c.Key(fmt.Sprintf("more-%d", i))).Gap(10).Padding(16, 20).Radius(10).Border(1, t.Border).Children(func() {
					ui.Text(c, "Follow-up: "+turn.Question).FontSize(16).Bold()
					if i == len(w.More)-1 && run != nil {
						ui.Spinner(c).Size(14, 14)
						return
					}
					a.answerView(c, turn)
				})
			}
			if w.Question != "" && !st.busy() {
				ui.Button(c, "Ask a follow-up").Tooltip("Continue this conversation in the side panel").OnClick(func() { a.openQuestion(w.ID) })
			}
			if w.Err != "" && run == nil {
				failureBox(c, w.Err)
			}
			if run == nil && w.Agent != "" {
				ui.Text(c, writtenBy(w.Agent, w.Model, w.Generated.Format("Jan 2 15:04"))).FontSize(12).TextColor(t.TextMuted)
			}
			a.askWalk(c, w.ID)
		})
	})
}

func writtenBy(p agent.Provider, model, when string) string {
	s := "By " + providerName(p)
	if model != "" {
		s += " (" + model + ")"
	}
	return s + ", " + when
}

// askWalk asks a question about a walkthrough as a whole, and lists the
// questions asked before.
func (a *app) askWalk(c *ui.Context, parent string) {
	t := c.Theme()
	key := a.questionDraftKey(nil, askTarget{parent, -1})
	draft := a.drafts[key]
	submit := func() {
		if q := strings.TrimSpace(draft); q != "" {
			a.drafts[key] = ""
			a.askQuestion(parent, -1, q)
		}
	}
	ui.Column(c).Gap(8).Margin(8, 0, 0, 0).Children(func() {
		ui.Text(c, "Ask about the changes").FontSize(18).Bold()
		ui.Text(c, "The answer opens in the side panel, as a short walkthrough. The agent starts from what it learned for this page, and you can ask follow-up questions there.").FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(c.Key("ask-"+key), &draft).Placeholder("Why did the agent change the cache key?").Grow(1).OnSubmit(submit)
			if in.Changed() {
				a.drafts[key] = draft
			}
			ui.PrimaryButton(c, "Ask").Disabled(strings.TrimSpace(draft) == "").OnClick(submit)
		})
		for _, w := range a.rev.WalksOn(parent, -1) {
			a.walkRow(c, w)
		}
	})
}
