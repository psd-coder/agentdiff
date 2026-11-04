package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/review"
)

// stamp is the date and time of an answer or a review, as "Nov 3 14:05".
func stamp(at time.Time) string {
	return at.Format("Jan 2 15:04")
}

// pageTitle is the title at the top of a page.
func pageTitle(ctx *ui.Context, text string) ui.Element {
	return ui.Text(ctx, text).FontSize(22).Bold()
}

// heading is the title of a dialog, or of a part of a page.
func heading(ctx *ui.Context, text string) ui.Element {
	return ui.Text(ctx, text).FontSize(18).Bold()
}

// sectionHeading names a part of a page, above its cards.
func sectionHeading(ctx *ui.Context, text string) {
	heading(ctx, text).Margin(8, 0, 0, 0)
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

// messageHeader names who wrote a message of a thread, and when.
func messageHeader(ctx *ui.Context, who string, at time.Time) {
	ui.Row(ctx).Gap(6).Children(func() {
		smallLabel(ctx, who)
		if !at.IsZero() {
			smallCaption(ctx, at.Format("15:04"))
		}
	})
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

// activityLine is a spinner with what the agent or the app does now.
func activityLine(ctx *ui.Context, text string) ui.Element {
	return ui.Row(ctx).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Spinner(ctx).Size(12, 12)
		caption(ctx, text).SingleLine().Shrink(1)
	})
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

// ghostButton is a small borderless button inside another control, as a
// chevron or the ⋯ button. Its hover tints what is under it, so it shows
// on a row that is itself hovered or chosen. Callers set its label and
// children, and act on Clicked.
func ghostButton(ctx *ui.Context) ui.Element {
	button := ui.ButtonBase(ctx).Padding(4).Radius(4)
	if button.Hovered() {
		button.Background(ctx.Theme().Text.Alpha(0.08))
	}
	return button
}

// disclosureArrow is the chevron of something that folds: it points down
// while open, and right while folded.
func disclosureArrow(ctx *ui.Context, open bool) ui.Element {
	arrow := ui.Icon(ctx, chevronIcon).FontSize(iconSmall).TextColor(ctx.Theme().TextMuted)
	if !open {
		arrow.Rotate(-90)
	}
	return arrow
}

// diffStat counts the added and the deleted lines, in green and red. A
// count of zero does not show. Callers set the font size.
func diffStat(ctx *ui.Context, additions, deletions int) ui.Element {
	theme := ctx.Theme()
	return ui.Row(ctx).Gap(4).Children(func() {
		if additions > 0 {
			ui.Text(ctx, fmt.Sprintf("+%d", additions)).TextColor(theme.Success)
		}
		if deletions > 0 {
			ui.Text(ctx, fmt.Sprintf("−%d", deletions)).TextColor(theme.Danger)
		}
	})
}

// countBadge shows a count in a badge, and nothing for zero.
func countBadge(ctx *ui.Context, count int) {
	if count > 0 {
		ui.Badge(ctx, fmt.Sprint(count))
	}
}

// noMatch tells that a filter left nothing in a list, as "No commands
// match".
func noMatch(ctx *ui.Context, things string) ui.Element {
	return caption(ctx, "No "+things+" match").Padding(6, 8)
}

// linkCard is a linkRow that stacks lines: what it is about, an excerpt,
// a meta line.
func linkCard(ctx *ui.Context) ui.Element {
	return linkRow(ctx).Padding(10, 12).Gap(4).Column().AlignItems(ui.Stretch)
}

// copyText puts text on the clipboard and tells it in a toast.
func copyText(ctx *ui.Context, text, toast string) {
	ctx.WriteClipboard(text)
	ctx.Toast(toast)
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

// panelFrame is the column of a side panel, on the right of the page.
// Callers set its children: a panelBar, the content, a panelFooter.
func panelFrame(ctx *ui.Context) ui.Element {
	theme := ctx.Theme()
	return ui.Column(ctx).Fill().Background(theme.Background).BorderWidth(0, 0, 0, 1).BorderColor(theme.Border)
}

// panelBar is the bar at the top of a side panel, with its title and its
// controls.
func panelBar(ctx *ui.Context) ui.Element {
	return ui.Row(ctx).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(ctx.Theme().Border)
}

// panelFooter is the bar at the foot of a side panel, with the actions on
// what the panel shows.
func panelFooter(ctx *ui.Context) ui.Element {
	return ui.Row(ctx).Gap(12).Padding(6, 12).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(ctx.Theme().Border)
}

// composer is the text box at the foot of a side panel. Its draft lives in
// app.drafts under key. Cmd+Enter calls send with the text, and send
// returns false when the panel cannot take it now, which keeps the text.
// buttons builds the controls after the key hint, with the draft and a
// submit that sends it.
func (app *app) composer(ctx *ui.Context, key, placeholder string, send func(text string) bool, buttons func(draft string, submit func())) {
	draft := app.drafts[key]
	submit := func(text string) bool {
		if strings.TrimSpace(text) == "" || !send(text) {
			return false
		}
		app.drafts[key] = ""
		return true
	}
	ui.Column(ctx).Gap(8).Padding(10, 12).BorderWidth(1, 0, 0, 0).BorderColor(ctx.Theme().Border).Children(func() {
		in := ui.TextArea(ctx.Key("draft-"+key), &draft).Lines(3, 10).Placeholder(placeholder).AutoFocus().
			OnTextKey(func(k ui.TextKey) bool {
				if k.Key != ui.KeyEnter || k.Mods != ui.Cmd {
					return false
				}
				if submit(k.Text()) {
					k.SetText("")
				}
				return true
			})
		if in.Changed() {
			app.drafts[key] = draft
		}
		ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
			smallCaption(ctx, app.platform.cmdEnter()+" asks the agent").Grow(1)
			buttons(draft, func() { submit(draft) })
		})
	})
}
