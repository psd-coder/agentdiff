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

// settingsTabs are the names and icons of the tabs, in the order of
// their constants.
var settingsTabs = []struct {
	name string
	icon *ui.SVG
}{
	{"Appearance", paletteIcon},
	{"Agent", botIcon},
	{"Prompts", textIcon},
	{"Shortcuts", keyboardIcon},
}

func (app *app) settingsModal(ctx *ui.Context) {
	form := app.settings
	if form == nil {
		return
	}
	theme := ctx.Theme()
	const radius float32 = 10
	ui.DialogBase(ctx, &form.open, func(back, panel ui.Element) {
		// The top stays in place as the tab changes, and the dialog is as
		// tall as the tab needs, leaving as much room under it as above.
		_, height := ctx.Size()
		gap := max(48, height*0.15)
		back.Background(ui.RGBA(0, 0, 0, 0.4)).Justify(ui.Start).Padding(gap, 16, gap, 16)
		panel.Width(720).Shrink(1).MinHeight(0).Radius(radius).Background(theme.Background).
			Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3)).Label("Settings")
		ui.Column(ctx).AlignItems(ui.Center).Gap(6).Padding(10, 16, 8, 16).Radius(radius, radius, 0, 0).Background(theme.Surface).Children(func() {
			ui.Text(ctx, settingsTabs[form.tab].name).Bold()
			app.settingsToolbar(ctx, form)
		})
		ui.Divider(ctx)
		ui.Column(ctx).Gap(14).Padding(20, 24).Shrink(1).MinHeight(0).Children(func() {
			switch form.tab {
			case agentTab:
				ui.Scroll(ctx).Shrink(1).MinHeight(0).FillWidth().Children(func() {
					ui.Column(ctx).PaddingEnd(4).Children(func() {
						app.agentFields(ctx, &form.agent, form.status, &form.ahead)
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
		ui.Divider(ctx)
		ui.Row(ctx).Gap(8).AlignItems(ui.Center).Padding(12, 20).Children(func() {
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

// settingsToolbar shows the tabs as the toolbar of a macOS settings
// window: an icon over each name, the chosen one in the accent color.
func (app *app) settingsToolbar(ctx *ui.Context, form *settingsForm) {
	theme := ctx.Theme()
	tabs := ui.TabsBase(ctx, &form.tab, len(settingsTabs))
	app.takeFocus(tabs.List.Gap(2).Label("Settings"), "settings")
	tabs.List.Children(func() {
		for i, tab := range settingsTabs {
			button := tabs.Tab(i).Padding(5, 10, 4, 10).Radius(6).TextColor(theme.TextMuted)
			switch {
			case i == form.tab:
				button.TextColor(theme.Accent).Background(theme.Text.Alpha(0.08))
			case button.Hovered():
				button.TextColor(theme.Text)
			}
			button.Children(func() {
				ui.Column(ctx).AlignItems(ui.Center).Gap(3).Children(func() {
					ui.Icon(ctx, tab.icon).FontSize(22)
					ui.Text(ctx, tab.name).SingleLine()
				})
			})
		}
	})
}

// settingsLabels are the labels of the fields of every tab.
var settingsLabels = []string{
	"Theme:", "Code theme:",
	"Agent:", "Model:", "Effort:", "Automatic model:", "Automatic effort:", "Write ahead:", "Binary:", "Extra arguments:", "Your setup:",
	"Prompt:", "Text:", "Blocks:",
}

// labeledForm is a form whose labels are as wide as the widest label of
// every tab, so the controls stay in place as the tab changes.
func labeledForm(ctx *ui.Context, fields func()) {
	ui.Form(ctx, func() {
		// A form makes its labels as wide as its widest, and these fields
		// show nothing but give their labels.
		ui.Box(ctx).Absolute().Invisible().Children(func() {
			for _, label := range settingsLabels {
				ui.Field(ctx.Key("width "+label), label, func() {})
			}
		})
		fields()
	})
}

// codePreview is the code the Appearance tab shows in the chosen code
// theme.
const codePreview = "```go\n// greet says hello to each name.\nfunc greet(names []string) int {\n\tfor i, n := range names {\n\t\tfmt.Printf(\"%d: hello, %s\\n\", i, n)\n\t}\n\treturn len(names)\n}\n```"

func (app *app) appearanceTab(ctx *ui.Context, form *settingsForm) {
	theme := ctx.Theme()
	labeledForm(ctx, func() {
		ui.Field(ctx, "Theme:", func() {
			ui.Segmented(ctx, &form.theme, "System", "Light", "Dark").Label("Theme").AlignSelf(ui.Start)
		})
		ui.Field(ctx, "Code theme:", func() {
			ui.Select(ctx, &form.codeTheme, codeThemeNames()).Label("Code theme").AlignSelf(ui.Start).MinWidth(200)
			ui.Text(ctx, "Colors code in diffs and in answers. A theme with only dark colors, as Dracula, also brings its own background.").FontSize(theme.FontSize - 1).TextColor(theme.TextMuted)
			markdown.View(ctx, codePreview, &app.md)
		})
	})
}

func (app *app) agentFields(ctx *ui.Context, form *agentForm, status map[agent.Provider]string, ahead *bool) {
	names := make([]string, len(providers))
	for i, p := range providers {
		names[i] = p.Name()
	}
	efforts := providers[form.provider].Efforts()
	setup := "The agent gets your own setup, as in a run you start yourself. Your hooks run outside the read-only rules of the app."
	if !form.userSetup {
		setup = providers[form.provider].WithoutUserSetup() + " The Prompts tab holds the instructions it gets."
	}
	labeledForm(ctx, func() {
		ui.Field(ctx, "Agent:", func() {
			ui.Segmented(ctx, &form.provider, names...).Label("Agent").AlignSelf(ui.Start)
		}).Description(status[providers[form.provider]])
		ui.Field(ctx, "Model:", func() {
			ui.TextInput(ctx, &form.model).Placeholder("CLI default").Label("Model")
		})
		ui.Field(ctx, "Effort:", func() {
			ui.Select(ctx, &form.effort, append([]string{defaultEffort}, efforts...)).Label("Effort").AlignSelf(ui.Start).MinWidth(200)
		})
		ui.Divider(ctx)
		ui.Field(ctx, "Automatic model:", func() {
			ui.TextInput(ctx, &form.autoModel).Placeholder("Same as above").Label("Model for automatic runs")
		})
		ui.Field(ctx, "Automatic effort:", func() {
			ui.Select(ctx, &form.autoEffort, append([]string{sameEffort}, efforts...)).Label("Effort for automatic runs").AlignSelf(ui.Start).MinWidth(200)
		}).Description("Automatic runs are the ones the app starts by itself: the overview and the deeper walkthroughs it writes ahead. Your questions and Go deeper use the model and effort above.")
		ui.Field(ctx, "Write ahead:", func() {
			ui.Checkbox(ctx, ahead, "Write the deeper walkthrough of every step with the overview")
		}).Description(fmt.Sprintf("Go deeper then opens at once. It costs one agent run per step, %d at a time.", maxAhead))
		ui.Divider(ctx)
		ui.Field(ctx, "Binary:", func() {
			ui.TextInput(ctx, &form.binary).Placeholder("Found on your shell's PATH").Label("Binary")
		})
		ui.Field(ctx, "Extra arguments:", func() {
			ui.TextInput(ctx, &form.extraArgs).Placeholder("Passed to the CLI as they are, split at spaces").Label("Extra arguments")
		})
		ui.Field(ctx, "Your setup:", func() {
			ui.Checkbox(ctx, &form.userSetup, "Load my own instructions, skills, hooks and MCP servers")
		}).Description(setup)
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
