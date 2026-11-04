package app

import (
	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/review"
)

// pageTitle is the title at the top of a page.
func pageTitle(ctx *ui.Context, text string) ui.Element {
	return ui.Text(ctx, text).FontSize(22).Bold()
}

// sectionHeading names a part of a page, above its cards.
func sectionHeading(ctx *ui.Context, text string) {
	ui.Text(ctx, text).FontSize(18).Bold().Margin(8, 0, 0, 0)
}

// cardTitle is the title of a card or of a note in the middle of a page.
func cardTitle(ctx *ui.Context, text string) ui.Element {
	return ui.Text(ctx, text).FontSize(16).Bold()
}

// caption is a line of secondary text: a note under a title, a meta line,
// what a run does now.
func caption(ctx *ui.Context, text string) ui.Element {
	return ui.Text(ctx, text).FontSize(12).TextColor(ctx.Theme().TextMuted)
}

// smallCaption is the secondary text of a dense row: a time, a count, a
// key hint.
func smallCaption(ctx *ui.Context, text string) ui.Element {
	return ui.Text(ctx, text).FontSize(11).TextColor(ctx.Theme().TextMuted)
}

// smallLabel names a group of rows, or who wrote a message.
func smallLabel(ctx *ui.Context, text string) ui.Element {
	return ui.Text(ctx, text).FontSize(11).Bold().TextColor(ctx.Theme().TextMuted)
}

// anchorLabel names the place a thread is about.
func anchorLabel(ctx *ui.Context, label string) {
	ui.Text(ctx, label).Font("monospace").FontSize(12).TextColor(ctx.Theme().Accent).SingleLine()
}

// bubble is a message the user wrote in a conversation.
func bubble(ctx *ui.Context, text string) {
	ui.Text(ctx, text).Selectable().Padding(8, 10).Radius(8).Background(ctx.Theme().Surface)
}

// codeQuote shows lines of code or of a prompt in a gray box. Callers set
// the font size.
func codeQuote(ctx *ui.Context, text string) ui.Element {
	return ui.Text(ctx, text).Font("monospace").Padding(6, 10).Radius(6).Background(ctx.Theme().Surface).Selectable()
}

// errorText shows why an answer did not come: the agent's error, or
// Stopped in gray for a run the user stopped.
func errorText(ctx *ui.Context, err string) ui.Element {
	if err == review.Stopped {
		return caption(ctx, err)
	}
	return ui.Text(ctx, err).FontSize(12).TextColor(ctx.Theme().Danger).Selectable()
}

// pageColumn holds the content of a page that scrolls. Callers set its
// gap and children.
func pageColumn(ctx *ui.Context) ui.Element {
	return ui.Column(ctx).Padding(24, 32).MaxWidth(980)
}

func cardBase(ctx *ui.Context) ui.Element {
	return ui.Column(ctx).Radius(10).Border(1, ctx.Theme().Border)
}

// card holds a main part of a page: a step, a section of the overview.
// Callers set its gap and children.
func card(ctx *ui.Context) ui.Element {
	return cardBase(ctx).Padding(16, 20).Background(ctx.Theme().Surface)
}

// outlineCard holds a smaller part of a page: an item of a section, a
// group of files, a follow-up. Callers set its gap and children.
func outlineCard(ctx *ui.Context) ui.Element {
	return cardBase(ctx).Padding(12, 16)
}

// linkRow is a bordered row that opens what it shows. Callers set its
// padding, direction and children, and act on Clicked.
func linkRow(ctx *ui.Context) ui.Element {
	theme := ctx.Theme()
	row := ui.ButtonBase(ctx).Radius(8).Border(1, theme.Border)
	if row.Hovered() {
		row.Background(theme.SurfaceHover)
	}
	return row
}

// failureBox shows the error of a page's answer in a red box, or Stopped
// in gray for a run the user stopped.
func failureBox(ctx *ui.Context, err string) {
	theme := ctx.Theme()
	if err == review.Stopped {
		errorText(ctx, err)
		return
	}
	ui.Column(ctx).Gap(4).Padding(12).Radius(8).Border(1, theme.Danger).Children(func() {
		ui.Text(ctx, "The agent failed").Bold().TextColor(theme.Danger)
		ui.Text(ctx, err).FontSize(12).Font("monospace").Selectable()
	})
}

// warningBox is the yellow box of a warning or a note the reader should
// not miss. Callers set its gap and children.
func warningBox(ctx *ui.Context) ui.Element {
	theme := ctx.Theme()
	return ui.Column(ctx).Padding(10, 14).Radius(8).Background(theme.Warning.Alpha(0.15)).Border(1, theme.Warning)
}

// centerNote fills the page with a title and the detail builds, in its
// middle.
func centerNote(ctx *ui.Context, title string, detail func()) {
	ui.Column(ctx).Grow(1).Center().Gap(8).Padding(24).Children(func() {
		cardTitle(ctx, title)
		detail()
	})
}

func errorState(ctx *ui.Context, title, detail string) {
	centerNote(ctx, title, func() {
		ui.Text(ctx, detail).TextColor(ctx.Theme().TextMuted).Selectable().MaxWidth(600)
	})
}

func emptyState(ctx *ui.Context, title, detail string) {
	centerNote(ctx, title, func() {
		ui.Text(ctx, detail).TextColor(ctx.Theme().TextMuted).MaxWidth(520).TextAlign(ui.Center)
	})
}
