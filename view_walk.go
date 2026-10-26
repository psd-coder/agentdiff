package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psdcoder/agentdiff/internal/agent"
	"github.com/psdcoder/agentdiff/internal/gitx"
	"github.com/psdcoder/agentdiff/internal/review"
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
	key := fmt.Sprintf("%s/%d", parent, i)
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
				ui.Button(c, "Deeper ›").Tooltip("Open the deeper walkthrough of this step").OnClick(func() { a.page = "walk:" + deeper.ID })
			} else {
				ui.PrimaryButton(c, "Go deeper").Tooltip("A walkthrough of this step, one level closer to the code").OnClick(func() { a.deeper(parent, i, "") })
			}
			ui.Button(c, "Ask about this step").OnClick(func() { a.stepAsk, a.stepDraft = key, "" })
		})
		if a.stepAsk == key {
			a.askField(c, "What do you want to know about this step?", func(q string) {
				a.stepAsk = ""
				a.deeper(parent, i, q)
			})
		}
		for _, w := range asked {
			a.walkRow(c, w)
		}
	})
}

// askField is a one-line question with Enter to send and Escape to
// cancel.
func (a *app) askField(c *ui.Context, placeholder string, send func(string)) {
	submit := func() {
		if q := strings.TrimSpace(a.stepDraft); q != "" {
			a.stepDraft = ""
			send(q)
		}
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c, &a.stepDraft).Placeholder(placeholder).AutoFocus().Grow(1).OnSubmit(submit)
		ui.PrimaryButton(c, "Ask").Disabled(strings.TrimSpace(a.stepDraft) == "").OnClick(submit)
		ui.Button(c, "Cancel").OnClick(func() { a.stepAsk = "" })
	})
	if c.Shortcut(0, ui.KeyEscape) {
		a.stepAsk = ""
	}
}

// walkRow links to a walk from the page above it.
func (a *app) walkRow(c *ui.Context, w *review.Walk) {
	t := c.Theme()
	row := ui.ButtonBase(c.Key("walk-"+w.ID)).Padding(8, 12).Gap(8).Radius(8).Border(1, t.Border).AlignItems(ui.Center).Label("Open walkthrough")
	if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		ui.Text(c, "›").TextColor(t.Accent)
		ui.Text(c, a.rev.WalkTitle(w)).SingleLine().Shrink(1).Grow(1)
		switch {
		case a.walkRuns[w.ID] != nil:
			ui.Spinner(c).Size(12, 12)
		case w.Err != "":
			ui.Text(c, "failed").FontSize(11).TextColor(t.Danger)
		}
	})
	if row.Clicked() {
		a.page = "walk:" + w.ID
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
	if d := a.diffs[r.File]; d != nil {
		if d.state == nil {
			return snippet{loading: d.loading, missing: d.err}
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
		return s
	}
	f := a.extraOf(r.File)
	if f.loading || f.err != "" {
		return snippet{loading: f.loading, missing: f.err}
	}
	return cut(len(f.lines), func(i int) string { return f.lines[i] }, r)
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
	f := &extraFile{loading: true}
	a.extra[path] = f
	r, s, gen := a.repo, a.scope, a.gen
	a.async(func() {
		_, text, err := r.Contents(context.Background(), s, gitx.FileChange{Path: path, Status: gitx.StatusAdded})
		a.update(func() {
			if gen != a.gen {
				return
			}
			f.loading = false
			if err != nil {
				f.err = err.Error()
				return
			}
			f.lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
		})
	})
	return f
}

func (a *app) refView(c *ui.Context, r review.Ref) {
	t := c.Theme()
	s := a.snippetOf(r)
	ui.Column(c).Radius(8).Border(1, t.Border).Clip().Children(func() {
		ui.Row(c).Gap(10).Padding(6, 10).AlignItems(ui.Center).Background(t.Surface).Children(func() {
			label := ui.Text(c, r.Label()).Font("monospace").FontSize(12).SingleLine().Shrink(0)
			if s.inDiff {
				label.TextColor(t.Accent).Cursor(ui.CursorPointer).Tooltip("Open in the diff")
				if label.Clicked() {
					a.openRef(r)
				}
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
	a.page = "file:" + r.File
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
	run := a.walkRuns[w.ID]
	path := a.rev.WalkPath(w.ID)
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Padding(24, 32).Gap(16).MaxWidth(980).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
				crumb := func(label, page string) {
					link := ui.Text(c, excerpt(label, 40)).FontSize(12).TextColor(t.Accent).Cursor(ui.CursorPointer)
					if link.Clicked() {
						a.page = page
					}
					ui.Text(c, "›").FontSize(12).TextColor(t.TextMuted)
				}
				crumb("Overview", "overview")
				for _, p := range path[:len(path)-1] {
					crumb(a.rev.WalkTitle(p), "walk:"+p.ID)
				}
				ui.Text(c, excerpt(a.rev.WalkTitle(w), 40)).FontSize(12).TextColor(t.TextMuted)
			})
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.rev.WalkTitle(w)).FontSize(22).Bold().Grow(1).Shrink(1)
				if run != nil {
					ui.Button(c, "Stop").OnClick(func() { a.stopWalk(w.ID) })
				} else {
					ui.Button(c, "Regenerate").OnClick(func() { a.regenerate(w.ID) })
				}
				ui.Button(c, "Log").Tooltip("What the agent did for this walkthrough").OnClick(func() { a.showLog(w.ID) })
				ui.Button(c, "Delete").Tooltip("Delete this walkthrough and the ones under it").OnClick(func() { a.deleteWalk(w) })
			})
			if s, ok := a.rev.StepOf(w); ok && w.Question != "" {
				ui.Text(c, "About the step: "+s.Title).FontSize(12).TextColor(t.TextMuted)
			}
			switch {
			case run != nil:
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, run.activity).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1)
				})
			case w.Content != nil:
				a.walkthrough(c, w.ID, w.Content)
			case w.Markdown != "":
				markdown.View(c, w.Markdown, &a.md)
			}
			if w.Err != "" && run == nil {
				ui.Column(c).Gap(4).Padding(12).Radius(8).Border(1, t.Danger).Children(func() {
					ui.Text(c, "The agent failed").Bold().TextColor(t.Danger)
					ui.Text(c, w.Err).FontSize(12).Font("monospace").Selectable()
				})
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
	submit := func() {
		if q := strings.TrimSpace(a.askDraft); q != "" {
			a.askDraft = ""
			a.deeper(parent, -1, q)
		}
	}
	ui.Column(c).Gap(8).Margin(8, 0, 0, 0).Children(func() {
		ui.Text(c, "Ask about the changes").FontSize(18).Bold()
		ui.Text(c, "The answer is a walkthrough of its own. The agent starts from what it learned for this page.").FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("ask-"+parent), &a.askDraft).Placeholder("Why did the agent change the cache key?").Grow(1).OnSubmit(submit)
			ui.PrimaryButton(c, "Ask").Disabled(strings.TrimSpace(a.askDraft) == "").OnClick(submit)
		})
		for _, w := range a.rev.WalksOn(parent, -1) {
			a.walkRow(c, w)
		}
	})
}
