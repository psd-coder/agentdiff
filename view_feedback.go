package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/psdcoder/agentdiff/internal/review"
)

const feedbackFile = "agentdiff-feedback.md"

func (a *app) exportOptions() review.ExportOptions {
	return review.ExportOptions{Agent: a.exportAgent, Resolved: a.exportDone}
}

func (a *app) feedback() string {
	return review.Export(a.rev, a.scope.Describe(), a.exportOptions())
}

func agentPrompt(path string) string {
	return fmt.Sprintf("Read the code review feedback in %s and address each item. If you disagree with an item, say why instead of changing the code.", path)
}

func (a *app) feedbackPage(c *ui.Context) {
	t := c.Theme()
	text := a.feedback()
	n := len(review.ExportedThreads(a.rev, a.exportOptions()))
	ui.Column(c).Fill().Children(func() {
		ui.Column(c).Gap(10).Padding(20, 24).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Text(c, "Feedback for the coding agent").FontSize(22).Bold()
			ui.Text(c, fmt.Sprintf("%d threads go into the feedback. Exclude a thread in its panel. Give the text to your coding agent: paste it, or save it to a file and paste the prompt that points to it.", n)).TextColor(t.TextMuted)
			ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c, &a.exportAgent, "Include agent replies")
				ui.Checkbox(c, &a.exportDone, "Include resolved threads")
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.PrimaryButton(c, "Copy feedback").Disabled(n == 0).OnClick(func() {
					c.WriteClipboard(a.feedback())
					c.Toast("Feedback copied")
				})
				ui.Button(c, "Save to file…").Disabled(n == 0).OnClick(func() { a.saveFeedback(c.Services()) })
				if a.savedPath != "" {
					ui.Button(c, "Copy prompt for agent").Tooltip(agentPrompt(a.savedPath)).OnClick(func() {
						c.WriteClipboard(agentPrompt(a.savedPath))
						c.Toast("Prompt copied")
					})
				}
			})
			if a.savedPath != "" {
				ui.Text(c, "Saved to "+a.savedPath).FontSize(12).TextColor(t.TextMuted).Selectable()
			}
		})
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Text(c, text).Font("monospace").FontSize(12).Padding(16, 24).Selectable()
		})
	})
}

// saveFeedback saves into the git directory by default: the agent working
// in the repository can read it, and it never shows up as a change.
func (a *app) saveFeedback(s ui.Services) {
	text, repo, rev := a.feedback(), a.repo, a.rev
	go func() {
		def := filepath.Join(repo.Root, feedbackFile)
		if dir, err := repo.GitDir(context.Background()); err == nil {
			def = filepath.Join(dir, feedbackFile)
		}
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: "Save feedback", DefaultPath: def})
		if err != nil || path == "" {
			return
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			mygo.Dialog.Error("Could not save the feedback", err.Error())
			return
		}
		a.update(func() {
			if a.rev == rev {
				a.savedPath = path
			}
			s.WriteClipboard(agentPrompt(path))
		})
	}()
}
