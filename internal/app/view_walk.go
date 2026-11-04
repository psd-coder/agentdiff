package app

import (
	"cmp"
	"fmt"
	"strings"
	"time"

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
	code    editor.Highlighter
	err     string
	loading bool
}

// reveal is a hunk to scroll to once the file's diff is on screen.
type reveal struct {
	file string
	hunk int
}

// walkthrough shows a summary, its steps and its sections. parent is the
// walk they belong to, empty for the overview.
func (app *app) walkthrough(ctx *ui.Context, parent string, walkthrough *review.Walkthrough) {
	if walkthrough.Summary != "" {
		ui.Text(ctx, walkthrough.Summary).FontSize(15).Selectable()
	}
	for i, s := range walkthrough.Steps {
		app.stepCard(ctx.Key(fmt.Sprintf("step-%s-%d", parent, i)), parent, i, s)
	}
	for si, sec := range walkthrough.Sections {
		if sec.Title != "" {
			sectionHeading(ctx.Key(fmt.Sprintf("part-%s-%d", parent, si)), sec.Title)
		}
		for i, it := range sec.Items {
			label := app.pathLabel(parent, sec.Title, cmp.Or(it.Title, fmt.Sprintf("Item %d", i+1)))
			discuss := func(menu *ui.Menu) { app.discussItem(menu, label, it.Text) }
			card := outlineCard(ctx.Key(fmt.Sprintf("part-%s-%d-%d", parent, si, i))).Gap(6)
			app.discussMenu(card, label, it.Text)
			card.Children(func() {
				ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(ctx, it.Title).Bold().Grow(1)
					app.sectionThreads(ctx, label, it.Text)
				})
				if it.Text != "" {
					markdown.View(ctx, it.Text, &app.md)
				}
				for ri, r := range it.Refs {
					app.refView(ctx.Key(fmt.Sprintf("ref-%d", ri)), r, discuss)
				}
			})
		}
	}
}

func (app *app) stepCard(ctx *ui.Context, parent string, index int, step review.Step) {
	card := card(ctx).Gap(10)
	label := app.pathLabel(parent, step.Title)
	app.discussMenu(card, label, step.Text)
	discuss := func(menu *ui.Menu) { app.discussItem(menu, label, step.Text) }
	card.Children(func() {
		ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
			cardTitle(ctx, fmt.Sprintf("%d. %s", index+1, step.Title)).Grow(1)
			app.sectionThreads(ctx, label, step.Text)
		})
		if step.Text != "" {
			markdown.View(ctx, step.Text, &app.md)
		}
		for ri, r := range step.Refs {
			app.refView(ctx.Key(fmt.Sprintf("ref-%d", ri)), r, discuss)
		}
		var deeper *review.Walk
		var asked []*review.Walk
		for _, w := range app.rev.WalksOn(parent, step.ID) {
			if w.Question == "" && deeper == nil {
				deeper = w
			} else {
				asked = append(asked, w)
			}
		}
		ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
			if deeper != nil {
				ui.Button(ctx, "Deeper ›").Tooltip("Open the deeper walkthrough of this step").OnClick(func() { app.nav.goTo(page{kindWalk, deeper.ID}) })
				if app.busy(deeper.ID) {
					ui.Spinner(ctx).Size(12, 12)
				}
			} else {
				ui.PrimaryButton(ctx, "Go deeper").Tooltip("A walkthrough of this step, one level closer to the code").OnClick(func() { app.deeper(parent, step.ID) })
			}
			ui.Button(ctx, "Ask about this step").Tooltip("Ask in the side panel").OnClick(func() { app.askAbout(parent, step.ID) })
		})
		for _, w := range asked {
			app.walkRow(ctx, w)
		}
	})
}

// walkRow links to a walk from the page above it.
func (app *app) walkRow(ctx *ui.Context, walk *review.Walk) {
	theme := ctx.Theme()
	row := linkRow(ctx.Key("walk-"+walk.ID)).Padding(8, 12).Gap(8).AlignItems(ui.Center)
	row.Children(func() {
		ui.Text(ctx, "›").TextColor(theme.Accent)
		ui.Text(ctx, app.rev.WalkTitle(walk)).SingleLine().Shrink(1).Grow(1)
		targetMark(ctx, app.state(walk.ID))
	})
	if row.Clicked() {
		if walk.Question != "" {
			app.openQuestion(walk.ID)
		} else {
			app.nav.goTo(page{kindWalk, walk.ID})
		}
	}
}

// pathLabel names a step or an item of a section for the threads about
// it: the titles of the walks above it, then its own titles. For an item
// of the overview, this is the section title and the item title.
func (app *app) pathLabel(parent string, titles ...string) string {
	var parts []string
	for _, w := range app.rev.WalkPath(parent) {
		parts = append(parts, app.rev.WalkTitle(w))
	}
	return strings.Join(append(parts, titles...), " › ")
}

// snippet is the lines a reference shows, with the ones the change
// touched marked.
type snippet struct {
	lines   []string
	changed []bool
	// code highlights the text the lines come from, where the first of
	// them is line first. It is nil for a file without a known language.
	code    editor.Highlighter
	first   int
	inDiff  bool
	loading bool
	missing string
}

func (app *app) snippetOf(ref review.Ref) snippet {
	if app.diffs[ref.File] == nil {
		app.readExtra(ref.File)
	}
	snippet, _ := app.loadedSnippet(ref)
	return snippet
}

// loadedSnippet is the snippet of a reference from the files read so
// far. It reports false when its file is not read yet.
func (app *app) loadedSnippet(ref review.Ref) (snippet, bool) {
	if diff := app.diffs[ref.File]; diff != nil {
		if diff.state == nil {
			return snippet{loading: diff.loading, missing: diff.err}, true
		}
		buf, code := diff.state.New, diff.newCode
		if ref.Side == review.SideOld {
			buf, code = diff.state.Old, diff.oldCode
		}
		lineCount := buf.LineCount()
		if lineCount > 0 && buf.Line(lineCount-1) == "" {
			lineCount--
		}
		snippet := cut(lineCount, buf.Line, ref)
		snippet.code, snippet.inDiff = code, true
		if snippet.missing == "" {
			hunks := diff.state.Hunks()
			for i := range snippet.lines {
				snippet.changed[i] = inHunk(hunks, ref.Side, ref.Start-1+i)
			}
		}
		return snippet, true
	}
	file := app.extra[ref.File]
	if file == nil {
		return snippet{}, false
	}
	if file.loading || file.err != "" {
		return snippet{loading: file.loading, missing: file.err}, true
	}
	snippet := cut(len(file.lines), func(i int) string { return file.lines[i] }, ref)
	snippet.code = file.code
	return snippet, true
}

// savedSnippet is the snippet of a reference from the code the review
// saved, for a review whose changes are gone.
func savedSnippet(ref review.Ref) snippet {
	if len(ref.Code) == 0 {
		return snippet{missing: "The code of this snippet was not saved with the review."}
	}
	var snippet snippet
	for _, l := range ref.Code {
		snippet.lines = append(snippet.lines, l.Text)
		snippet.changed = append(snippet.changed, l.Changed)
	}
	snippet.code = codeHighlighter(ref.File, strings.Join(snippet.lines, "\n"))
	return snippet
}

func cut(count int, line func(int) string, ref review.Ref) snippet {
	if ref.Start > count {
		return snippet{missing: fmt.Sprintf("The file has %d lines", count)}
	}
	snippet := snippet{first: ref.Start - 1}
	for i := ref.Start - 1; i < min(ref.End, count); i++ {
		snippet.lines = append(snippet.lines, line(i))
	}
	snippet.changed = make([]bool, len(snippet.lines))
	return snippet
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

// readExtra reads a file outside the changes on its first use. The
// first load has no snapshot yet, and the view asks again once it shows.
func (app *app) readExtra(path string) {
	if app.extra[path] != nil || app.snap == nil {
		return
	}
	app.extra[path] = &extraFile{loading: true}
	app.loader.readFile(app.snap, path)
}

// newExtraFile is a file outside the changes, read as text, or failing
// with err.
func newExtraFile(path, text string, err error) *extraFile {
	if err != nil {
		return &extraFile{err: err.Error()}
	}
	return &extraFile{lines: strings.Split(strings.TrimSuffix(text, "\n"), "\n"), code: codeHighlighter(path, text)}
}

// refView shows the snippet of a reference. discuss adds the items of the
// part the snippet is in to the menu of its lines, when the part has any.
func (app *app) refView(ctx *ui.Context, ref review.Ref, discuss func(*ui.Menu)) {
	app.snippetView(ctx, ref, app.snippetOf(ref), discuss)
}

// snippetView shows the lines of a reference under its label. The label
// opens the diff when the lines are in it.
func (app *app) snippetView(ctx *ui.Context, ref review.Ref, snippet snippet, discuss func(*ui.Menu)) {
	theme := ctx.Theme()
	ui.Column(ctx).Radius(8).Border(1, theme.Border).Clip().Children(func() {
		ui.Row(ctx).Gap(10).Padding(6, 10).AlignItems(ui.Center).Background(theme.Surface).Children(func() {
			if snippet.inDiff {
				if textLink(ctx, ref.Label(), func(e ui.Element) { e.Font("monospace").FontSize(12) }).Shrink(0).Tooltip("Open in the diff").Clicked() {
					app.openRef(ref)
				}
			} else {
				ui.Text(ctx, ref.Label()).Font("monospace").FontSize(12).SingleLine().Shrink(0)
			}
			if ref.Why != "" {
				caption(ctx, ref.Why).SingleLine().Shrink(1)
			}
		})
		switch {
		case snippet.loading:
			ui.Spinner(ctx).Size(12, 12).Margin(8, 10)
		case snippet.missing != "":
			caption(ctx, snippet.missing).Padding(6, 10)
		}
		if len(snippet.lines) > 0 {
			app.snippetLines(ctx, ref, snippet, discuss)
		}
	})
}

// highlightBudget is how long a snippet waits for the lines above it to
// be tokenized. The highlighter keeps the states of the lines it
// tokenized, so a file is only tokenized once.
const highlightBudget = 100 * time.Millisecond

// tokens are the tokens of each line of the snippet, tokenized from the
// top of its text, or nil for plain text.
func (snippet snippet) tokens() [][]editor.Token {
	out := make([][]editor.Token, len(snippet.lines))
	if snippet.code == nil {
		return out
	}
	snippet.code.Highlight(snippet.first+len(snippet.lines)-1, highlightBudget)
	for i, line := range snippet.lines {
		out[i], _ = snippet.code.Tokens(snippet.first+i, line, nil)
	}
	return out
}

// snippetLines shows the highlighted lines of a snippet. Their text is
// selectable, and their numbers are not.
func (app *app) snippetLines(ctx *ui.Context, ref review.Ref, snippet snippet, discuss func(*ui.Menu)) {
	theme := ctx.Theme()
	colors := app.codeColors
	numberColor := theme.TextMuted
	lines := ui.Column(ctx).Selectable()
	if codeThemeNamed(app.cfg.CodeTheme).ownBackground() {
		lines.Background(colors.Background)
		numberColor = colors.LineNumber
	}
	if discuss != nil {
		// The menu of selectable text replaces the menu of the card around
		// it, so it repeats the card's items.
		lines.ContextMenu(func(menu *ui.Menu) {
			discuss(menu)
			menu.Separator()
			menu.EditItems()
		})
	}
	tokens := snippet.tokens()
	mark := theme.Success.Alpha(0.14)
	if ref.Side == review.SideOld {
		mark = theme.Danger.Alpha(0.14)
	}
	lines.Children(func() {
		for i, line := range snippet.lines {
			row := ui.Row(ctx.Key(i)).Gap(10).Padding(1, 10)
			if snippet.changed[i] {
				row.Background(mark)
			}
			row.Children(func() {
				ui.Text(ctx, fmt.Sprint(ref.Start+i)).Font("monospace").FontSize(12).TextColor(numberColor).Width(32).TextAlign(ui.End).Unselectable()
				ui.RichText(ctx, codeSpans(line, tokens[i], colors)...).Font("monospace").FontSize(12).SingleLine().Shrink(1)
			})
		}
	})
}

// codeSpans colors the tokens of a line of code. Tabs show as four
// spaces.
func codeSpans(line string, tokens []editor.Token, colors *editor.Theme) []ui.Span {
	spans := make([]ui.Span, 0, len(tokens)+1)
	add := func(text string, kind editor.TokenKind) {
		style := colors.Tokens[kind]
		span := ui.Span{Text: strings.ReplaceAll(text, "\t", "    "), Color: style.Color, Italic: style.Italic}
		if style.Bold {
			span.Weight = 700
		}
		spans = append(spans, span)
	}
	start := 0
	for _, tok := range tokens {
		end := min(tok.End, len(line))
		if end > start {
			add(line[start:end], tok.Kind)
			start = end
		}
	}
	if start < len(line) || len(spans) == 0 {
		add(line[start:], editor.TokenText)
	}
	return spans
}

// openRef shows a reference in its file's diff, with its lines selected.
func (app *app) openRef(ref review.Ref) {
	diff := app.diffs[ref.File]
	if diff == nil || diff.state == nil {
		return
	}
	app.nav.goTo(page{kindFile, ref.File})
	diff.state.SetSelectedLines(editor.DiffLineRange{Side: diffSide(ref.Side), Start: ref.Start - 1, End: ref.End - 1}, true)
	app.reveal = &reveal{file: ref.File, hunk: nearestHunk(diff.state.Hunks(), ref)}
}

// nearestHunk is the hunk holding the reference's first line, or the
// closest one to it.
func nearestHunk(hunks []editor.DiffHunk, ref review.Ref) int {
	best, dist := -1, 0
	line := ref.Start - 1
	for i, h := range hunks {
		lo, hi := h.NewStart, h.NewEnd
		if ref.Side == review.SideOld {
			lo, hi = h.OldStart, h.OldEnd
		}
		hunkDistance := 0
		switch {
		case line < lo:
			hunkDistance = lo - line
		case line >= hi:
			hunkDistance = line - hi + 1
		}
		if best < 0 || hunkDistance < dist {
			best, dist = i, hunkDistance
		}
	}
	return best
}

func (app *app) walkPage(ctx *ui.Context, id string) {
	walk := app.rev.Walk(id)
	if walk == nil {
		app.overviewPage(ctx, "")
		return
	}
	run := app.runner.run(app.rev, walk.ID)
	st := app.state(walk.ID)
	path := app.rev.WalkPath(walk.ID)
	version := app.versionOn(page{kindWalk, walk.ID})
	app.pageScroll(ctx, page{kindWalk, walk.ID}).Grow(1).FillWidth().Children(func() {
		pageColumn(ctx).Gap(16).Children(func() {
			ui.Row(ctx).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
				crumb := func(label string, p page) {
					link := textLink(ctx, excerpt(label, 40), func(e ui.Element) { e.FontSize(12) }).Tooltip(label)
					if link.Clicked() {
						app.nav.goTo(p)
					}
					caption(ctx, "›")
				}
				crumb(app.pageLabel(app.versionPage(version)), app.versionPage(version))
				for _, p := range path[:len(path)-1] {
					crumb(app.rev.WalkTitle(p), page{kindWalk, p.ID})
				}
				caption(ctx, excerpt(app.rev.WalkTitle(walk), 40))
			})
			ui.Row(ctx).Gap(12).AlignItems(ui.Center).Children(func() {
				pageTitle(ctx, app.rev.WalkTitle(walk)).Grow(1).Shrink(1)
				app.cmdButton(ctx, "stop-walk", walk.ID, "Stop")
				app.moreButton(ctx, walk.ID, "regenerate-walk", "walk-log", "", "delete-walk")
			})
			if step, ok := app.rev.StepOf(walk); ok && walk.Question != "" {
				caption(ctx, "About the step: "+step.Title)
			}
			app.staleBanner(ctx, version)
			switch {
			case run != nil && len(walk.More) == 0:
				activityLine(ctx, run.activity)
			case st.status == targetQueued:
				ui.Row(ctx).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Spinner(ctx).Size(12, 12)
					caption(ctx, fmt.Sprintf("Waiting: the app writes %d walkthroughs at a time.", maxAhead))
					ui.Button(ctx, "Start now").OnClick(func() { app.runner.startNow(app.rev, walk.ID) })
				})
			case walk.Content != nil:
				app.walkthrough(ctx, walk.ID, walk.Content)
			case walk.Markdown != "":
				markdown.View(ctx, walk.Markdown, &app.md)
			}
			for i, turn := range walk.More {
				outlineCard(ctx.Key(fmt.Sprintf("more-%d", i))).Gap(10).Children(func() {
					cardTitle(ctx, "Follow-up: "+turn.Question)
					if i == len(walk.More)-1 && run != nil {
						ui.Spinner(ctx).Size(14, 14)
						return
					}
					app.answerView(ctx, turn)
				})
			}
			if walk.Question != "" && !st.busy() {
				ui.Button(ctx, "Ask a follow-up").Tooltip("Continue this conversation in the side panel").OnClick(func() { app.openQuestion(walk.ID) })
			}
			if walk.Err != "" && run == nil {
				failureBox(ctx, walk.Err)
			}
			if run == nil && walk.Agent != "" {
				caption(ctx, writtenBy(walk.Agent, walk.Model, walk.Generated.Format("Jan 2 15:04")))
			}
			app.askWalk(ctx, walk.ID)
		})
	})
}

func writtenBy(provider agent.Provider, model, when string) string {
	byline := "By " + provider.Name()
	if model != "" {
		byline += " (" + model + ")"
	}
	return byline + ", " + when
}

// overviewBy says who wrote the overview, when, and with which prompt.
func overviewBy(ov *review.Overview) string {
	byline := writtenBy(ov.Agent, ov.Model, ov.Generated.Format("Jan 2 15:04"))
	if from := promptFrom(ov.Prompt); from != "" {
		byline += ", " + from
	}
	return byline
}

// askWalk asks a question about a walkthrough as a whole, and lists the
// questions asked before.
func (app *app) askWalk(ctx *ui.Context, parent string) {
	key := app.questionDraftKey(nil, askTarget{parent, ""})
	draft := app.drafts[key]
	submit := func() {
		if question := strings.TrimSpace(draft); question != "" {
			app.drafts[key] = ""
			app.askQuestion(parent, "", question)
		}
	}
	ui.Column(ctx).Gap(8).Margin(8, 0, 0, 0).Children(func() {
		heading(ctx, "Ask about the changes")
		caption(ctx, "The answer opens in the side panel, as a short walkthrough. The agent starts from what it learned for this page, and you can ask follow-up questions there.")
		ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
			in := ui.TextInput(ctx.Key("ask-"+key), &draft).Placeholder("Why did the agent change the cache key?").Grow(1).OnSubmit(submit)
			if in.Changed() {
				app.drafts[key] = draft
			}
			ui.PrimaryButton(ctx, "Ask").Disabled(strings.TrimSpace(draft) == "").OnClick(submit)
		})
		for _, w := range app.rev.WalksOn(parent, "") {
			app.walkRow(ctx, w)
		}
	})
}
