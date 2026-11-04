package app

import (
	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/review"
)

// errorText shows why an answer did not come: the agent's error, or
// Stopped in gray for a run the user stopped.
func errorText(ctx *ui.Context, err string) {
	theme := ctx.Theme()
	if err == review.Stopped {
		ui.Text(ctx, err).FontSize(12).TextColor(theme.TextMuted)
		return
	}
	ui.Text(ctx, err).FontSize(12).TextColor(theme.Danger).Selectable()
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

func errorState(ctx *ui.Context, title, detail string) {
	theme := ctx.Theme()
	ui.Column(ctx).Grow(1).Center().Gap(8).Padding(24).Children(func() {
		ui.Text(ctx, title).FontSize(16).Bold()
		ui.Text(ctx, detail).TextColor(theme.TextMuted).Selectable().MaxWidth(600)
	})
}

func emptyState(ctx *ui.Context, title, detail string) {
	theme := ctx.Theme()
	ui.Column(ctx).Grow(1).Center().Gap(8).Padding(24).Children(func() {
		ui.Text(ctx, title).FontSize(16).Bold()
		ui.Text(ctx, detail).TextColor(theme.TextMuted).MaxWidth(520).TextAlign(ui.Center)
	})
}
