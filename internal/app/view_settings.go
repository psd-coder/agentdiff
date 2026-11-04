package app

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

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

// agentForm holds how to run one agent.
type agentForm struct {
	model      string
	effort     string
	autoModel  string
	autoEffort string
	command    string
	extraArgs  string
	userSetup  bool
}

func newAgentForm(cfg agent.Config) agentForm {
	form := agentForm{
		model: cfg.Model, effort: cmp.Or(cfg.Effort, defaultEffort),
		autoModel: cfg.AutoModel, autoEffort: cmp.Or(cfg.AutoEffort, sameEffort),
		command: agent.JoinWords(cfg.Command), extraArgs: strings.Join(cfg.ExtraArgs, " "), userSetup: cfg.UserSetup,
	}
	return form
}

func (form agentForm) config(provider agent.Provider) agent.Config {
	return agent.Config{
		Provider:   provider,
		Model:      strings.TrimSpace(form.model),
		Effort:     effortOf(provider, form.effort),
		AutoModel:  strings.TrimSpace(form.autoModel),
		AutoEffort: effortOf(provider, form.autoEffort),
		Command:    agent.SplitWords(form.command),
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
	tab int
	// titled is the tab the title of the window names, plus one, or zero
	// before the window has a title.
	titled int
	status map[agent.Provider]string
	// checks counts the checks of each agent's CLI, so only the result of
	// the last one shows.
	checks  map[agent.Provider]int
	prompts promptForm
	settingsValues
	// saved is what the form held when it last saved.
	saved settingsValues
}

// settingsValues are the fields of the settings form. The agent form
// holds the fields of the chosen provider.
type settingsValues struct {
	provider  int
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

// The width of the settings window, and the heights it can take: it is
// as tall as the open tab needs, between the two.
const (
	settingsWidth     = 720
	settingsMinHeight = 240
	settingsMaxHeight = 680
)

// openSettings opens the settings window, or brings it to the front.
func (app *app) openSettings() {
	if app.settings != nil {
		if app.settingsWin != nil {
			app.settingsWin.Focus()
		}
		return
	}
	form := &settingsForm{
		status:    map[agent.Provider]string{},
		checks:    map[agent.Provider]int{},
		prompts:   app.newPromptForm(),
		provider:  max(slices.Index(providers, app.cfg.Provider), 0),
		agent:     newAgentForm(app.cfg.Agent()),
		ahead:     app.cfg.DeeperAhead,
		theme:     themeIndex(app.cfg.Theme),
		codeTheme: codeThemeNamed(app.cfg.CodeTheme).name,
	}
	form.saved = form.settingsValues
	app.settings = form
	if app.win == nil {
		return
	}
	app.settingsWin = mygo.NewWindow(mygo.WindowOptions{
		Title:             settingsTabs[form.tab].name,
		Width:             settingsWidth,
		Height:            settingsMaxHeight,
		StateKey:          "settings",
		TitleBarStyle:     app.platform.titleBar(mygo.TitleBarHidden),
		AutoHideMenuBar:   true,
		DisableResize:     true,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableFullScreen: true,
		Content:           ui.View(app.settingsView),
	})
	app.settingsWin.OnClosed(func() { app.update(app.settingsClosed) })
	// The window stays invisible until it has the height of its tab, so
	// it does not show at another height first. If it never settles, it
	// shows anyway.
	app.settingsWin.SetOpacity(0)
	time.AfterFunc(300*time.Millisecond, func() { app.update(app.revealSettings) })
	for _, p := range providers {
		app.checkAgent(form, app.cfg.AgentOf(p), 0)
	}
}

// commandCheckDelay is how long a changed command stays the same before
// the settings check it, so they do not run a command half typed.
const commandCheckDelay = 800 * time.Millisecond

// checkAgent shows under the tab of the agent the version of the CLI that
// cfg runs, checked after delay. A later check of the agent replaces it.
func (app *app) checkAgent(form *settingsForm, cfg agent.Config, delay time.Duration) {
	provider := cfg.Provider
	form.checks[provider]++
	check := form.checks[provider]
	form.status[provider] = "Checking…"
	current := func() bool { return app.settings == form && form.checks[provider] == check }
	start := func() {
		go func() {
			path, version, err := agent.Available(context.Background(), cfg)
			msg := fmt.Sprintf("%s (%s)", version, path)
			if err != nil {
				msg = "Not found: " + err.Error()
			}
			app.update(func() {
				if current() {
					form.status[provider] = msg
				}
			})
		}()
	}
	if delay == 0 {
		start()
		return
	}
	time.AfterFunc(delay, func() {
		app.update(func() {
			if current() {
				start()
			}
		})
	})
}

// openSettingsTab opens the settings window on a tab.
func (app *app) openSettingsTab(tab int) {
	app.openSettings()
	app.settings.tab = tab
}

// saveSettings saves what the settings form holds, as the user changes it.
func (app *app) saveSettings() {
	form := app.settings
	themeChanged := form.theme != form.saved.theme
	if form.agent != form.saved.agent {
		edited := form.agent.config(providers[form.saved.provider])
		app.cfg.SetAgent(edited)
		if form.agent.command != form.saved.agent.command && app.settingsWin != nil {
			app.checkAgent(form, edited, commandCheckDelay)
		}
	}
	if form.provider != form.saved.provider {
		app.cfg.Provider = providers[form.provider]
		form.agent = newAgentForm(app.cfg.Agent())
	}
	form.saved = form.settingsValues
	app.cfg.DeeperAhead = form.ahead
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
	if app.win != nil {
		// The main window shows the agent and the code theme.
		app.win.Invalidate()
	}
}

// closeSettings closes the settings window.
func (app *app) closeSettings() {
	if app.settingsWin != nil {
		app.settingsWin.Close()
		return
	}
	app.settingsClosed()
}

// settingsClosed forgets the settings once their window closed.
func (app *app) settingsClosed() {
	app.settings, app.settingsWin, app.recording = nil, nil, ""
	app.settingsShown = false
}

// revealSettings makes the settings window visible, once.
func (app *app) revealSettings() {
	if app.settingsWin != nil && !app.settingsShown {
		app.settingsShown = true
		app.settingsWin.SetOpacity(1)
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

// settingsView is the settings window: the tabs as its toolbar, under its
// title, and the open tab. Changes save as the user makes them.
func (app *app) settingsView(ctx *ui.Context) {
	app.updateMenu()
	form := app.settings
	if form == nil {
		return
	}
	theme := ctx.Theme()
	name := settingsTabs[form.tab].name
	if app.settingsWin != nil && form.titled != form.tab+1 {
		app.settingsWin.SetTitle(name)
		form.titled = form.tab + 1
	}
	top := float32(8)
	if app.platform.mac {
		top = 0
	}
	ui.Column(ctx).AlignItems(ui.Center).Gap(4).Padding(top, 16, 8, 16).Background(theme.Surface).DragWindow().Bind(&app.settingsHeader).Children(func() {
		// On Windows and Linux the title bar of the system shows the title.
		if app.platform.mac {
			ui.Row(ctx).Height(max(ctx.TitleBar().Height, 28)).AlignItems(ui.Center).Children(func() {
				ui.Text(ctx, name).Bold()
			})
		}
		app.settingsToolbar(ctx, form)
	})
	ui.Divider(ctx)
	if form.tab == shortcutsTab {
		// The list scrolls under the filter, which stays in place.
		ui.Column(ctx).Gap(14).Padding(20, 0).Grow(1).MinHeight(0).Children(func() { app.shortcutsTab(ctx) })
	} else {
		// The scroll spans the window, so its bar shows at the window's
		// edge, outside the padding of the tab.
		ui.Scroll(ctx).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(ctx).Gap(14).Padding(20, 24).Bind(&app.settingsBody).Children(func() {
				switch form.tab {
				case agentTab:
					app.agentFields(ctx, form)
				case promptsTab:
					app.promptsTab(ctx, &form.prompts)
				case appearanceTab:
					app.appearanceTab(ctx, form)
				}
			})
		})
	}
	app.fitSettings(ctx, form.tab)
	if form.settingsValues != form.saved {
		app.saveSettings()
	}
	app.savePrompts(&form.prompts)
}

// fitSettings makes the settings window as tall as the open tab needs,
// up to settingsMaxHeight, as the settings windows of Mac apps do. Its
// top edge stays in place. The Shortcuts tab, a long list, takes the
// most room.
func (app *app) fitSettings(ctx *ui.Context, tab int) {
	if app.settingsWin == nil {
		return
	}
	// The boxes of the last frame lag one change behind this build, so
	// each build asks for one more, which measures what it laid out.
	app.settingsSettled = !app.settingsSettled
	if !app.settingsSettled {
		ctx.Invalidate()
	}
	height := float32(settingsMaxHeight)
	if tab != shortcutsTab {
		body := app.settingsBody.Bounds(ctx)
		if body.H == 0 {
			return
		}
		height = min(max(app.settingsHeader.Bounds(ctx).H+1+body.H, settingsMinHeight), settingsMaxHeight)
	}
	if _, current := ctx.Size(); math.Abs(float64(height-current)) < 1 {
		app.revealSettings()
		return
	}
	win, want := app.settingsWin, int(math.Ceil(float64(height)))
	app.win.Update(func() { win.SetContentSize(settingsWidth, want) })
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
					ui.Icon(ctx, tab.icon).FontSize(iconTab)
					ui.Text(ctx, tab.name).SingleLine()
				})
			})
		}
	})
}

// settingsLabels are the labels of the fields of every tab.
var settingsLabels = []string{
	"Theme:", "Code theme:",
	"Agent:", "Model:", "Effort:", "Automatic model:", "Automatic effort:", "Write ahead:", "Command:", "Extra arguments:", "Your setup:",
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
			popUp(ctx, &form.codeTheme, codeThemeNames()).Label("Code theme")
			ui.Text(ctx, "Colors code in diffs and in answers. A theme with only dark colors, as Dracula, also brings its own background.").FontSize(theme.FontSize - 1).TextColor(theme.TextMuted)
			markdown.View(ctx, codePreview, &app.md)
		})
	})
}

// commandHelp says what the Command field takes. For an agent that needs
// the macOS sandbox to stay read-only, it warns when the system has no such
// sandbox, or when the sandbox does not reach a CLI that a launcher starts.
func commandHelp(sys platform, provider agent.Provider, command []string) string {
	help := fmt.Sprintf("The name or path of the CLI. To run it in a container, put the command that runs it there in front, as dev %[1]s or devcontainer exec --workspace-folder . %[1]s.", provider)
	switch {
	case !provider.Sandboxed():
	case !sys.mac:
		help += fmt.Sprintf(" %s runs without a sandbox on this system, so only its permission rules keep it read-only. A shell redirect in a pipeline gets past them.", provider.Name())
	case len(command) > 1:
		help += fmt.Sprintf(" The macOS sandbox does not reach into the container, so only the permission rules keep %s read-only there. A shell redirect in a pipeline gets past them.", provider.Name())
	}
	return help
}

func (app *app) agentFields(ctx *ui.Context, settings *settingsForm) {
	names := make([]string, len(providers))
	for i, p := range providers {
		names[i] = p.Name()
	}
	form, provider := &settings.agent, providers[settings.provider]
	efforts := provider.Efforts()
	setup := "The agent gets your own setup, as in a run you start yourself. Your hooks run outside the read-only rules of the app."
	if !form.userSetup {
		setup = provider.WithoutUserSetup() + " The Prompts tab holds the instructions it gets."
	}
	labeledForm(ctx, func() {
		ui.Field(ctx, "Agent:", func() {
			ui.Segmented(ctx, &settings.provider, names...).Label("Agent").AlignSelf(ui.Start)
		}).Description(settings.status[provider])
		ui.Field(ctx, "Model:", func() {
			ui.TextInput(ctx, &form.model).Placeholder(cmp.Or(provider.ModelExample(), "CLI default")).Label("Model")
		}).Description(provider.ModelHelp())
		ui.Field(ctx, "Effort:", func() {
			popUp(ctx, &form.effort, append([]string{defaultEffort}, efforts...)).Label("Effort")
		})
		ui.Divider(ctx)
		ui.Field(ctx, "Automatic model:", func() {
			ui.TextInput(ctx, &form.autoModel).Placeholder("Same as above").Label("Model for automatic runs")
		})
		ui.Field(ctx, "Automatic effort:", func() {
			popUp(ctx, &form.autoEffort, append([]string{sameEffort}, efforts...)).Label("Effort for automatic runs")
		}).Description("Automatic runs are the ones the app starts by itself: the overview and the deeper walkthroughs it writes ahead. Your questions and Go deeper use the model and effort above.")
		ui.Field(ctx, "Write ahead:", func() {
			ui.Checkbox(ctx, &settings.ahead, "Write the deeper walkthrough of every step with the overview")
		}).Description(fmt.Sprintf("Go deeper then opens at once. It costs one agent run per step, %d at a time.", maxAhead))
		ui.Divider(ctx)
		ui.Field(ctx, "Command:", func() {
			ui.TextInput(ctx, &form.command).Placeholder(string(provider) + ", found on your shell's PATH").Label("Command")
		}).Description(commandHelp(app.platform, provider, agent.SplitWords(form.command)))
		ui.Field(ctx, "Extra arguments:", func() {
			ui.TextInput(ctx, &form.extraArgs).Placeholder("Passed to the CLI as they are, split at spaces").Label("Extra arguments")
		})
		ui.Field(ctx, "Your setup:", func() {
			ui.Checkbox(ctx, &form.userSetup, "Load my own instructions, skills, hooks and MCP servers")
		}).Description(setup)
	})
}
