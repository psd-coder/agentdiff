package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/psd-coder/agentdiff/internal/agent"
)

var providers = agent.Providers()

const (
	defaultEffort = "CLI default"
	sameEffort    = "Same as above"
)

type agentForm struct {
	provider   int
	model      string
	effort     string
	autoModel  string
	autoEffort string
	binary     string
	extraArgs  string
	userSetup  bool
}

func newAgentForm(cfg agent.Config) agentForm {
	form := agentForm{
		model: cfg.Model, effort: cmp.Or(cfg.Effort, defaultEffort),
		autoModel: cfg.AutoModel, autoEffort: cmp.Or(cfg.AutoEffort, sameEffort),
		binary: cfg.Binary, extraArgs: strings.Join(cfg.ExtraArgs, " "), userSetup: cfg.UserSetup,
		provider: max(slices.Index(providers, cfg.Provider), 0),
	}
	return form
}

func (form agentForm) config() agent.Config {
	provider := providers[form.provider]
	return agent.Config{
		Provider:   provider,
		Model:      strings.TrimSpace(form.model),
		Effort:     effortOf(provider, form.effort),
		AutoModel:  strings.TrimSpace(form.autoModel),
		AutoEffort: effortOf(provider, form.autoEffort),
		Binary:     strings.TrimSpace(form.binary),
		ExtraArgs:  strings.Fields(form.extraArgs),
		UserSetup:  form.userSetup,
	}
}

// effortOf is the effort picked in the form, empty when it is the
// default choice or a level the provider's CLI does not take.
func effortOf(provider agent.Provider, picked string) string {
	if slices.Contains(provider.Efforts(), picked) {
		return picked
	}
	return ""
}

type settingsForm struct {
	open    bool
	tab     int
	status  map[agent.Provider]string
	prompts promptForm
	settingsValues
	// saved is what the form held when it last saved.
	saved settingsValues
}

// settingsValues are the fields of the settings form.
type settingsValues struct {
	agent     agentForm
	ahead     bool
	theme     int
	codeTheme string
}

// The tabs of the settings.
const (
	appearanceTab = iota
	agentTab
	promptsTab
	shortcutsTab
)

var themes = []mygo.ThemeSource{mygo.ThemeSystem, mygo.ThemeLight, mygo.ThemeDark}

// themeIndex is the place of the saved theme in themes, the system one
// when the config holds none or one the app does not know.
func themeIndex(saved string) int {
	return max(0, slices.Index(themes, mygo.ThemeSource(saved)))
}

// applyTheme gives the window the saved theme.
func (app *app) applyTheme() {
	mygo.Theme.SetSource(themes[themeIndex(app.cfg.Theme)])
}

func (app *app) openSettings() {
	form := &settingsForm{
		open:      true,
		status:    map[agent.Provider]string{},
		prompts:   app.newPromptForm(),
		agent:     newAgentForm(app.cfg.Agent),
		ahead:     app.cfg.DeeperAhead,
		theme:     themeIndex(app.cfg.Theme),
		codeTheme: codeThemeNamed(app.cfg.CodeTheme).name,
	}
	form.saved = form.settingsValues
	app.settings = form
	if app.win == nil {
		return
	}
	for _, p := range providers {
		form.status[p] = "Checking…"
		app.async(func() {
			path, version, err := agent.Available(context.Background(), agent.Config{Provider: p})
			msg := fmt.Sprintf("%s (%s)", version, path)
			if err != nil {
				msg = "Not found: " + err.Error()
			}
			app.update(func() { form.status[p] = msg })
		})
	}
}

// saveSettings saves what the settings form holds, as the user changes it.
func (app *app) saveSettings() {
	form := app.settings
	themeChanged := form.theme != form.saved.theme
	form.saved = form.settingsValues
	app.cfg.Agent, app.cfg.DeeperAhead = form.agent.config(), form.ahead
	app.cfg.Theme = ""
	if form.theme > 0 {
		app.cfg.Theme = string(themes[form.theme])
	}
	app.cfg.CodeTheme = ""
	if form.codeTheme != codeThemes[0].name {
		app.cfg.CodeTheme = form.codeTheme
	}
	app.saveConfig()
	if themeChanged && app.win != nil {
		app.applyTheme()
	}
}

func (app *app) settingsModal(ctx *ui.Context) {
	form := app.settings
	if form == nil {
		return
	}
	theme := ctx.Theme()
	ui.DialogBase(ctx, &form.open, func(back, panel ui.Element) {
		// The top stays in place as the tab changes, and the dialog is as
		// tall as the tab needs, leaving as much room under it as above.
		_, height := ctx.Size()
		gap := max(48, height*0.15)
		back.Background(ui.RGBA(0, 0, 0, 0.4)).Justify(ui.Start).Padding(gap, 16, gap, 16)
		panel.Width(640).Shrink(1).MinHeight(0).Padding(20).Radius(10).Background(theme.Background).
			Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3)).Label("Settings")
		ui.Column(ctx).Gap(14).Padding(0, 0, 14, 0).Children(func() {
			ui.Text(ctx, "Settings").FontSize(18).Bold()
			app.takeFocus(ui.Tabs(ctx, &form.tab, "Appearance", "Agent", "Prompts", "Shortcuts").Label("Settings"), "settings")
		})
		ui.Column(ctx).Gap(14).Shrink(1).MinHeight(0).Children(func() {
			switch form.tab {
			case agentTab:
				ui.Scroll(ctx).Shrink(1).MinHeight(0).FillWidth().Children(func() {
					ui.Column(ctx).Gap(14).PaddingEnd(4).Children(func() {
						app.agentFields(ctx, &form.agent, form.status)
						ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Switch(ctx, &form.ahead).Label("Write the deeper walkthrough of every step with the overview")
							ui.Text(ctx, "Write the deeper walkthrough of every step with the overview")
						})
						ui.Text(ctx, fmt.Sprintf("Go deeper then opens at once. It costs one agent run per step, %d at a time.", maxAhead)).FontSize(11).TextColor(theme.TextMuted)
					})
				})
			case promptsTab:
				app.promptsTab(ctx, &form.prompts)
			case shortcutsTab:
				app.shortcutsTab(ctx)
			case appearanceTab:
				app.appearanceTab(ctx, form)
			}
		})
		ui.Row(ctx).Gap(8).AlignItems(ui.Center).Padding(14, 0, 0, 0).Children(func() {
			ui.Text(ctx, "Changes are saved as you make them.").FontSize(11).TextColor(theme.TextMuted).Grow(1)
			ui.PrimaryButton(ctx, "Done").Tooltip("Escape").OnClick(func() { form.open = false })
		})
	})
	if !form.open && app.recording != "" {
		// Escape cancels the recording, and the dialog stays.
		form.open, app.recording = true, ""
	}
	if form.settingsValues != form.saved {
		app.saveSettings()
	}
	app.savePrompts(&form.prompts)
	if !form.open {
		app.settings = nil
	}
}

// codePreview is the code the Appearance tab shows in the chosen code
// theme.
const codePreview = "```go\n// greet says hello to each name.\nfunc greet(names []string) int {\n\tfor i, n := range names {\n\t\tfmt.Printf(\"%d: hello, %s\\n\", i, n)\n\t}\n\treturn len(names)\n}\n```"

func (app *app) appearanceTab(ctx *ui.Context, form *settingsForm) {
	theme := ctx.Theme()
	ui.Column(ctx).Gap(14).Children(func() {
		ui.Column(ctx).Gap(4).Children(func() {
			ui.Text(ctx, "Theme").FontSize(12).Bold()
			ui.Segmented(ctx, &form.theme, "System", "Light", "Dark").Label("Theme")
		})
		ui.Column(ctx).Gap(4).Children(func() {
			ui.Text(ctx, "Code theme").FontSize(12).Bold()
			ui.Select(ctx, &form.codeTheme, codeThemeNames()).Label("Code theme")
			ui.Text(ctx, "Colors code in diffs and in answers. A theme with only dark colors, as Dracula, also brings its own background.").FontSize(11).TextColor(theme.TextMuted)
			markdown.View(ctx, codePreview, &app.md)
		})
	})
}

func (app *app) agentFields(ctx *ui.Context, form *agentForm, status map[agent.Provider]string) {
	theme := ctx.Theme()
	field := func(label string, build func()) {
		ui.Column(ctx).Gap(4).Children(func() {
			ui.Text(ctx, label).FontSize(12).Bold()
			build()
		})
	}
	field("Agent", func() {
		names := make([]string, len(providers))
		for i, p := range providers {
			names[i] = p.Name()
		}
		ui.Segmented(ctx, &form.provider, names...).Label("Agent")
		ui.Text(ctx, status[providers[form.provider]]).FontSize(11).TextColor(theme.TextMuted).SingleLine()
	})
	efforts := providers[form.provider].Efforts()
	pair := func(left, right func()) {
		ui.Row(ctx).Gap(12).Children(func() {
			ui.Column(ctx).Grow(1).Basis(0).Children(left)
			ui.Column(ctx).Grow(1).Basis(0).Children(right)
		})
	}
	pair(func() {
		field("Model", func() {
			ui.TextInput(ctx, &form.model).Placeholder("CLI default").Label("Model")
		})
	}, func() {
		field("Effort", func() {
			ui.Select(ctx, &form.effort, append([]string{defaultEffort}, efforts...)).Label("Effort")
		})
	})
	pair(func() {
		field("Model for automatic runs", func() {
			ui.TextInput(ctx, &form.autoModel).Placeholder("Same as above").Label("Model for automatic runs")
		})
	}, func() {
		field("Effort for automatic runs", func() {
			ui.Select(ctx, &form.autoEffort, append([]string{sameEffort}, efforts...)).Label("Effort for automatic runs")
		})
	})
	ui.Text(ctx, "Automatic runs are the ones the app starts by itself: the overview and the deeper walkthroughs it writes ahead. Your questions and Go deeper use the model and effort above.").FontSize(11).TextColor(theme.TextMuted)
	field("Binary", func() {
		ui.TextInput(ctx, &form.binary).Placeholder("Found on your shell's PATH").Label("Binary")
	})
	field("Extra arguments", func() {
		ui.TextInput(ctx, &form.extraArgs).Placeholder("Passed to the CLI as they are, split at spaces").Label("Extra arguments")
	})
	ui.Column(ctx).Gap(4).Children(func() {
		ui.Row(ctx).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Switch(ctx, &form.userSetup).Label("Load my own instructions, skills, hooks and MCP servers")
			ui.Text(ctx, "Load my own instructions, skills, hooks and MCP servers")
		})
		note := "The agent gets your own setup, as in a run you start yourself. Your hooks run outside the read-only rules of the app."
		if !form.userSetup {
			note = providers[form.provider].WithoutUserSetup() + " The Prompts tab holds the instructions it gets."
		}
		ui.Text(ctx, note).FontSize(11).TextColor(theme.TextMuted)
	})
}

func (app *app) chooseRepo() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: app.win, Title: "Open a git repository", Directory: true})
		if err != nil || len(paths) == 0 {
			return
		}
		app.update(func() { app.openRepo(paths[0]) })
	}()
}

func (app *app) welcome(ctx *ui.Context) {
	theme := ctx.Theme()
	ui.Column(ctx).Fill().Center().Gap(16).Padding(32).Background(theme.Background).Children(func() {
		ui.Text(ctx, "AgentDiff").FontSize(30).Bold()
		ui.Text(ctx, "Review what your coding agent changed: an explanation first, then the diff, grouped, with comments you send back.").TextColor(theme.TextMuted).MaxWidth(480).TextAlign(ui.Center)
		ui.PrimaryButton(ctx, "Open repository…").OnClick(app.chooseRepo)
		if app.repoErr != "" {
			ui.Text(ctx, app.repoErr).TextColor(theme.Danger).MaxWidth(480).Selectable()
		}
		if app.saveErr != "" {
			ui.Text(ctx, "Could not save the settings: "+app.saveErr).TextColor(theme.Danger).MaxWidth(480).Selectable()
		}
		if len(app.cfg.Recent) > 0 {
			ui.Column(ctx).Gap(4).Width(420).Margin(12, 0, 0, 0).Children(func() {
				ui.Text(ctx, "Recent").FontSize(12).Bold().TextColor(theme.TextMuted).Padding(0, 8)
				app.projectList(ctx, &app.projectNav, "", false, func(i int) { app.openRepo(app.cfg.Recent[i]) })
			})
		}
	})
}
