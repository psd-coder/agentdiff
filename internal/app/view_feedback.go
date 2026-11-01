package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/review"
)

const feedbackFile = "agentdiff-feedback.md"

func (app *app) exportOptions() review.ExportOptions {
	return review.ExportOptions{Agent: app.exportAgent, Resolved: app.exportDone}
}

func (app *app) feedback() string {
	return review.Export(app.rev, app.scope.Describe(), app.exportOptions())
}

func agentPrompt(path string) string {
	return fmt.Sprintf("Read the code review feedback in %s and address each item. If you disagree with an item, say why instead of changing the code.", path)
}

func (app *app) feedbackPage(ctx *ui.Context) {
	theme := ctx.Theme()
	text := app.feedback()
	threadCount := len(review.ExportedThreads(app.rev, app.exportOptions()))
	ui.Column(ctx).Fill().Children(func() {
		ui.Column(ctx).Gap(10).Padding(20, 24).BorderWidth(0, 0, 1, 0).BorderColor(theme.Border).Children(func() {
			ui.Text(ctx, "Feedback for the coding agent").FontSize(22).Bold()
			ui.Text(ctx, fmt.Sprintf("%d threads go into the feedback. Exclude a thread in its panel. Give the text to your coding agent: paste it, or save it to a file and paste the prompt that points to it.", threadCount)).TextColor(theme.TextMuted)
			ui.Row(ctx).Gap(16).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(ctx, &app.exportAgent, "Include agent replies")
				ui.Checkbox(ctx, &app.exportDone, "Include resolved threads")
			})
			ui.Row(ctx).Gap(8).Children(func() {
				app.cmdPrimaryButton(ctx, "copy-feedback", "", "Copy feedback")
				app.cmdButton(ctx, "save-feedback", "", "Save to file…")
				app.cmdButton(ctx, "copy-agent-prompt", "", "Copy prompt for agent")
			})
			if app.savedPath != "" {
				ui.Text(ctx, "Saved to "+app.savedPath).FontSize(12).TextColor(theme.TextMuted).Selectable()
			}
		})
		app.pageScroll(ctx, page{kind: kindFeedback}).Grow(1).FillWidth().Children(func() {
			ui.Text(ctx, text).Font("monospace").FontSize(12).Padding(16, 24).Selectable()
		})
	})
}

// saveFeedback saves into the git directory by default: the agent working
// in the repository can read it, and it never shows up as a change.
func (app *app) saveFeedback(services ui.Services) {
	text, repo, rev := app.feedback(), app.repo, app.rev
	go func() {
		def := filepath.Join(repo.Root, feedbackFile)
		if dir, err := repo.GitDir(context.Background()); err == nil {
			def = filepath.Join(dir, feedbackFile)
		}
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: app.win, Title: "Save feedback", DefaultPath: def})
		if err != nil || path == "" {
			return
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			app.showError("Could not save the feedback", err)
			return
		}
		app.update(func() {
			if app.rev == rev {
				app.savedPath = path
			}
			services.WriteClipboard(agentPrompt(path))
		})
	}()
}
