package app

import (
	"maps"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/review"
)

// promptForm holds the prompts of the settings while they are open.
type promptForm struct {
	// picked is the title of the prompt the tab shows.
	picked string
	// texts are the texts of the prompts, by their place in
	// review.Templates. A blank text sends the default prompt.
	texts []string
	// blocks tells which blocks show their text, by prompt and block.
	blocks map[string]*bool
	// repo holds the prompts that the open repository gives in place of
	// the ones in the settings.
	repo review.Prompts
}

func (app *app) newPromptForm() promptForm {
	form := promptForm{picked: review.Templates[0].Title, blocks: map[string]*bool{}}
	for _, t := range review.Templates {
		text := app.cfg.Prompts[t.Name]
		if strings.TrimSpace(text) == "" {
			text = t.Default
		}
		form.texts = append(form.texts, text)
	}
	if app.repo != nil {
		form.repo, app.promptProblems = readRepoPrompts(app.repo.Root)
	}
	return form
}

// savePrompts saves the prompts of the form that work. A blank text or
// the default one leaves the prompt out of the config. A text with a
// problem keeps the prompt the config holds.
func (app *app) savePrompts(form *promptForm) {
	next := review.Prompts{}
	for i, t := range review.Templates {
		text := form.texts[i]
		switch {
		case strings.TrimSpace(text) == "" || text == t.Default:
		case t.Check(text) == nil:
			next[t.Name] = text
		case app.cfg.Prompts[t.Name] != "":
			next[t.Name] = app.cfg.Prompts[t.Name]
		}
	}
	if maps.Equal(next, app.cfg.Prompts) {
		return
	}
	app.cfg.Prompts = next
	if len(next) == 0 {
		app.cfg.Prompts = nil
	}
	app.saveConfig()
}

func (app *app) promptsTab(ctx *ui.Context, form *promptForm) {
	theme := ctx.Theme()
	titles := make([]string, len(review.Templates))
	for i, tpl := range review.Templates {
		titles[i] = tpl.Title
	}
	pickedIndex := max(slices.Index(titles, form.picked), 0)
	tpl := review.Templates[pickedIndex]
	ui.Scroll(ctx).Shrink(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(ctx).Gap(14).PaddingEnd(4).Children(func() {
			ui.Column(ctx).Gap(4).Children(func() {
				ui.Text(ctx, "Prompt").FontSize(12).Bold()
				ui.Select(ctx, &form.picked, titles).Label("Prompt")
				ui.Text(ctx, tpl.About).FontSize(11).TextColor(theme.TextMuted)
			})
			if _, ok := form.repo[tpl.Name]; ok {
				ui.Text(ctx, "This repository gives its own prompt in "+repoPromptFile(tpl.Name)+", so its runs use that file. The text below goes to the runs in other repositories.").FontSize(12).
					Padding(10, 14).Radius(8).Background(theme.Warning.Alpha(0.15)).Border(1, theme.Warning)
			}
			ui.Column(ctx.Key("prompt-" + string(tpl.Name))).Gap(6).Children(func() {
				ui.TextArea(ctx, &form.texts[pickedIndex]).Lines(8, 16).Font("monospace").FontSize(12).Label(tpl.Title + " prompt")
				if text := form.texts[pickedIndex]; strings.TrimSpace(text) != "" {
					if err := tpl.Check(text); err != nil {
						ui.Text(ctx, "Not saved. "+err.Error()).FontSize(12).TextColor(theme.Danger).Selectable()
					}
				}
				ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(ctx, "An empty prompt sends the default one. Changes are saved while the prompt has no problems.").FontSize(11).TextColor(theme.TextMuted).Grow(1).Shrink(1)
					ui.Button(ctx, "Copy default").Tooltip("Copy the default text of this prompt, as a start for " + repoPromptFile(tpl.Name)).OnClick(func() {
						ctx.WriteClipboard(tpl.Default)
						ctx.Toast("Default prompt copied")
					})
					ui.Button(ctx, "Reset").Disabled(form.texts[pickedIndex] == tpl.Default).OnClick(func() { form.texts[pickedIndex] = tpl.Default })
				})
			})
			if len(tpl.Blocks) == 0 {
				ui.Text(ctx, "This prompt has no blocks.").FontSize(12).TextColor(theme.TextMuted)
				return
			}
			ui.Column(ctx.Key("blocks-" + string(tpl.Name))).Gap(8).Children(func() {
				ui.Text(ctx, "Blocks").FontSize(12).Bold()
				ui.Text(ctx, "The app puts its own text in place of each block. A prompt must keep its required blocks.").FontSize(11).TextColor(theme.TextMuted)
				for _, b := range tpl.Blocks {
					app.blockView(ctx.Key(b.Name), form, tpl.Name, b)
				}
			})
		})
	})
}

// blockView shows a block of a prompt, and the text the app puts in its
// place where the open review gives it one.
func (app *app) blockView(ctx *ui.Context, form *promptForm, name review.PromptName, block review.Block) {
	theme := ctx.Theme()
	need := "optional"
	if block.Required {
		need = "required"
	}
	ui.Column(ctx).Gap(4).Padding(8, 12).Radius(8).Border(1, theme.Border).Children(func() {
		ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(ctx, "{{"+block.Name+"}}").Font("monospace").FontSize(12).Bold().Selectable()
			ui.Text(ctx, need).FontSize(11).TextColor(theme.TextMuted)
		})
		ui.Text(ctx, block.About).FontSize(12)
		text, ok := app.blockText(name, block.Name)
		if !ok {
			return
		}
		key := string(name) + "/" + block.Name
		if form.blocks[key] == nil {
			form.blocks[key] = new(bool)
		}
		ui.Collapsible(ctx, "Show the text", form.blocks[key], func() {
			ui.Text(ctx, text).Font("monospace").FontSize(11).Selectable().Padding(6, 8).Radius(6).Background(theme.Surface)
		})
	})
}

// blockText is the text the app puts in place of a block: the format of
// the answer, or a value of the review on screen.
func (app *app) blockText(name review.PromptName, block string) (string, bool) {
	newRev := ""
	if app.repo != nil {
		newRev = app.scope.NewRev()
	}
	switch {
	case block == "format":
		return review.FormatText(name, newRev), true
	case app.repo == nil:
		return "", false
	case block == "scope":
		return app.scope.Describe(), true
	case block == "diffstat" && app.diffstat != "":
		return app.diffstat, true
	case block == "files" && len(app.files) > 0:
		return review.FileList(app.fileNames()), true
	}
	return "", false
}
