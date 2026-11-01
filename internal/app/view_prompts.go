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

func (a *app) newPromptForm() promptForm {
	f := promptForm{picked: review.Templates[0].Title, blocks: map[string]*bool{}}
	for _, t := range review.Templates {
		text := a.cfg.Prompts[t.Name]
		if strings.TrimSpace(text) == "" {
			text = t.Default
		}
		f.texts = append(f.texts, text)
	}
	if a.repo != nil {
		f.repo, a.promptProblems = readRepoPrompts(a.repo.Root)
	}
	return f
}

// savePrompts saves the prompts of the form that work. A blank text or
// the default one leaves the prompt out of the config. A text with a
// problem keeps the prompt the config holds.
func (a *app) savePrompts(f *promptForm) {
	next := review.Prompts{}
	for i, t := range review.Templates {
		text := f.texts[i]
		switch {
		case strings.TrimSpace(text) == "" || text == t.Default:
		case t.Check(text) == nil:
			next[t.Name] = text
		case a.cfg.Prompts[t.Name] != "":
			next[t.Name] = a.cfg.Prompts[t.Name]
		}
	}
	if maps.Equal(next, a.cfg.Prompts) {
		return
	}
	a.cfg.Prompts = next
	if len(next) == 0 {
		a.cfg.Prompts = nil
	}
	a.saveConfig()
}

func (a *app) promptsTab(c *ui.Context, f *promptForm) {
	t := c.Theme()
	titles := make([]string, len(review.Templates))
	for i, tpl := range review.Templates {
		titles[i] = tpl.Title
	}
	i := max(slices.Index(titles, f.picked), 0)
	tpl := review.Templates[i]
	ui.Scroll(c).Shrink(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).Gap(14).PaddingEnd(4).Children(func() {
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "Prompt").FontSize(12).Bold()
				ui.Select(c, &f.picked, titles).Label("Prompt")
				ui.Text(c, tpl.About).FontSize(11).TextColor(t.TextMuted)
			})
			if _, ok := f.repo[tpl.Name]; ok {
				ui.Text(c, "This repository gives its own prompt in "+repoPromptFile(tpl.Name)+", so its runs use that file. The text below goes to the runs in other repositories.").FontSize(12).
					Padding(10, 14).Radius(8).Background(t.Warning.Alpha(0.15)).Border(1, t.Warning)
			}
			ui.Column(c.Key("prompt-" + string(tpl.Name))).Gap(6).Children(func() {
				ui.TextArea(c, &f.texts[i]).Lines(8, 16).Font("monospace").FontSize(12).Label(tpl.Title + " prompt")
				if text := f.texts[i]; strings.TrimSpace(text) != "" {
					if err := tpl.Check(text); err != nil {
						ui.Text(c, "Not saved. "+err.Error()).FontSize(12).TextColor(t.Danger).Selectable()
					}
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "An empty prompt sends the default one. Changes are saved while the prompt has no problems.").FontSize(11).TextColor(t.TextMuted).Grow(1).Shrink(1)
					ui.Button(c, "Copy default").Tooltip("Copy the default text of this prompt, as a start for " + repoPromptFile(tpl.Name)).OnClick(func() {
						c.WriteClipboard(tpl.Default)
						c.Toast("Default prompt copied")
					})
					ui.Button(c, "Reset").Disabled(f.texts[i] == tpl.Default).OnClick(func() { f.texts[i] = tpl.Default })
				})
			})
			if len(tpl.Blocks) == 0 {
				ui.Text(c, "This prompt has no blocks.").FontSize(12).TextColor(t.TextMuted)
				return
			}
			ui.Column(c.Key("blocks-" + string(tpl.Name))).Gap(8).Children(func() {
				ui.Text(c, "Blocks").FontSize(12).Bold()
				ui.Text(c, "The app puts its own text in place of each block. A prompt must keep its required blocks.").FontSize(11).TextColor(t.TextMuted)
				for _, b := range tpl.Blocks {
					a.blockView(c.Key(b.Name), f, tpl.Name, b)
				}
			})
		})
	})
}

// blockView shows a block of a prompt, and the text the app puts in its
// place where the open review gives it one.
func (a *app) blockView(c *ui.Context, f *promptForm, name review.PromptName, b review.Block) {
	t := c.Theme()
	need := "optional"
	if b.Required {
		need = "required"
	}
	ui.Column(c).Gap(4).Padding(8, 12).Radius(8).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "{{"+b.Name+"}}").Font("monospace").FontSize(12).Bold().Selectable()
			ui.Text(c, need).FontSize(11).TextColor(t.TextMuted)
		})
		ui.Text(c, b.About).FontSize(12)
		text, ok := a.blockText(name, b.Name)
		if !ok {
			return
		}
		key := string(name) + "/" + b.Name
		if f.blocks[key] == nil {
			f.blocks[key] = new(bool)
		}
		ui.Collapsible(c, "Show the text", f.blocks[key], func() {
			ui.Text(c, text).Font("monospace").FontSize(11).Selectable().Padding(6, 8).Radius(6).Background(t.Surface)
		})
	})
}

// blockText is the text the app puts in place of a block: the format of
// the answer, or a value of the review on screen.
func (a *app) blockText(name review.PromptName, block string) (string, bool) {
	newRev := ""
	if a.repo != nil {
		newRev = a.scope.NewRev()
	}
	switch {
	case block == "format":
		return review.FormatText(name, newRev), true
	case a.repo == nil:
		return "", false
	case block == "scope":
		return a.scope.Describe(), true
	case block == "diffstat" && a.diffstat != "":
		return a.diffstat, true
	case block == "files" && len(a.files) > 0:
		return review.FileList(a.fileNames()), true
	}
	return "", false
}
