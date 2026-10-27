package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/psdcoder/agentdiff/internal/agent"
	"github.com/psdcoder/agentdiff/internal/review"
)

var providers = []agent.Provider{agent.Claude, agent.Codex}

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
	questions  string
}

func newAgentForm(cfg agent.Config, questions string) agentForm {
	f := agentForm{
		model: cfg.Model, effort: cmp.Or(cfg.Effort, defaultEffort),
		autoModel: cfg.AutoModel, autoEffort: cmp.Or(cfg.AutoEffort, sameEffort),
		binary: cfg.Binary, extraArgs: strings.Join(cfg.ExtraArgs, " "), questions: questions,
	}
	if cfg.Provider == agent.Codex {
		f.provider = 1
	}
	return f
}

func (f agentForm) config() agent.Config {
	p := providers[f.provider]
	return agent.Config{
		Provider:   p,
		Model:      strings.TrimSpace(f.model),
		Effort:     effortOf(p, f.effort),
		AutoModel:  strings.TrimSpace(f.autoModel),
		AutoEffort: effortOf(p, f.autoEffort),
		Binary:     strings.TrimSpace(f.binary),
		ExtraArgs:  strings.Fields(f.extraArgs),
	}
}

// effortOf is the effort picked in the form, empty when it is the
// default choice or a level the provider's CLI does not take.
func effortOf(p agent.Provider, picked string) string {
	if slices.Contains(agent.Efforts(p), picked) {
		return picked
	}
	return ""
}

type settingsForm struct {
	open   bool
	tab    int
	status map[agent.Provider]string
	settingsValues
	// saved is what the form held when it last saved.
	saved settingsValues
}

// settingsValues are the fields of the settings form.
type settingsValues struct {
	agent agentForm
	ahead bool
}

// The tabs of the settings.
const (
	agentTab = iota
	shortcutsTab
)

func (a *app) openSettings() {
	f := &settingsForm{
		open:   true,
		status: map[agent.Provider]string{},
		settingsValues: settingsValues{
			agent: newAgentForm(a.cfg.Agent, a.cfg.Questions),
			ahead: a.cfg.DeeperAhead,
		},
	}
	f.saved = f.settingsValues
	a.settings = f
	if a.win == nil {
		return
	}
	for _, p := range providers {
		f.status[p] = "Checking…"
		a.async(func() {
			path, version, err := agent.Available(context.Background(), agent.Config{Provider: p})
			msg := fmt.Sprintf("%s (%s)", version, path)
			if err != nil {
				msg = "Not found: " + err.Error()
			}
			a.update(func() { f.status[p] = msg })
		})
	}
}

// saveSettings saves what the settings form holds, as the user changes it.
func (a *app) saveSettings() {
	f := a.settings
	f.saved = f.settingsValues
	a.cfg.Agent, a.cfg.Questions, a.cfg.DeeperAhead = f.agent.config(), f.agent.questions, f.ahead
	a.saveConfig()
}

func (a *app) settingsModal(c *ui.Context) {
	f := a.settings
	if f == nil {
		return
	}
	t := c.Theme()
	ui.DialogBase(c, &f.open, func(back, panel ui.Element) {
		// The top stays in place as the tab changes, and the dialog is as
		// tall as the tab needs, up to the window's height.
		back.Background(ui.RGBA(0, 0, 0, 0.4)).Justify(ui.Start).Padding(48, 16, 20, 16)
		panel.Width(640).Shrink(1).MinHeight(0).Padding(20).Radius(10).Background(t.Background).
			Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.3)).Label("Settings")
		ui.Column(c).Gap(14).Padding(0, 0, 14, 0).Children(func() {
			ui.Text(c, "Settings").FontSize(18).Bold()
			ui.Tabs(c, &f.tab, "Agent", "Shortcuts")
		})
		ui.Column(c).Gap(14).Shrink(1).MinHeight(0).Children(func() {
			switch f.tab {
			case agentTab:
				ui.Scroll(c).Shrink(1).MinHeight(0).FillWidth().Children(func() {
					ui.Column(c).Gap(14).PaddingEnd(4).Children(func() {
						a.agentFields(c, &f.agent, f.status)
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Switch(c, &f.ahead).Label("Write the deeper walkthrough of every step with the overview")
							ui.Text(c, "Write the deeper walkthrough of every step with the overview")
						})
						ui.Text(c, fmt.Sprintf("Go deeper then opens at once. It costs one agent run per step, %d at a time.", maxAhead)).FontSize(11).TextColor(t.TextMuted)
					})
				})
			case shortcutsTab:
				a.shortcutsTab(c)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(14, 0, 0, 0).Children(func() {
			ui.Text(c, "Changes are saved as you make them.").FontSize(11).TextColor(t.TextMuted).Grow(1)
			ui.PrimaryButton(c, "Done").Tooltip("Escape").OnClick(func() { f.open = false })
		})
	})
	if !f.open && a.recording != "" {
		// Escape cancels the recording, and the dialog stays.
		f.open, a.recording = true, ""
	}
	if f.settingsValues != f.saved {
		a.saveSettings()
	}
	if !f.open {
		a.settings = nil
	}
}

func (a *app) agentFields(c *ui.Context, f *agentForm, status map[agent.Provider]string) {
	t := c.Theme()
	field := func(label string, build func()) {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, label).FontSize(12).Bold()
			build()
		})
	}
	field("Agent", func() {
		ui.Segmented(c, &f.provider, "Claude Code", "Codex").Label("Agent")
		ui.Text(c, status[providers[f.provider]]).FontSize(11).TextColor(t.TextMuted).SingleLine()
	})
	efforts := agent.Efforts(providers[f.provider])
	pair := func(left, right func()) {
		ui.Row(c).Gap(12).Children(func() {
			ui.Column(c).Grow(1).Basis(0).Children(left)
			ui.Column(c).Grow(1).Basis(0).Children(right)
		})
	}
	pair(func() {
		field("Model", func() {
			ui.TextInput(c, &f.model).Placeholder("CLI default").Label("Model")
		})
	}, func() {
		field("Effort", func() {
			ui.Select(c, &f.effort, append([]string{defaultEffort}, efforts...)).Label("Effort")
		})
	})
	pair(func() {
		field("Model for automatic runs", func() {
			ui.TextInput(c, &f.autoModel).Placeholder("Same as above").Label("Model for automatic runs")
		})
	}, func() {
		field("Effort for automatic runs", func() {
			ui.Select(c, &f.autoEffort, append([]string{sameEffort}, efforts...)).Label("Effort for automatic runs")
		})
	})
	ui.Text(c, "Automatic runs are the ones the app starts by itself: the overview and the deeper walkthroughs it writes ahead. Your questions and Go deeper use the model and effort above.").FontSize(11).TextColor(t.TextMuted)
	field("Binary", func() {
		ui.TextInput(c, &f.binary).Placeholder("Found on your shell's PATH").Label("Binary")
	})
	field("Extra arguments", func() {
		ui.TextInput(c, &f.extraArgs).Placeholder("Passed to the CLI as they are, split at spaces").Label("Extra arguments")
	})
	field("Checklist questions", func() {
		ui.TextArea(c, &f.questions).Lines(6, 14).Label("Checklist questions")
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "The overview answers these under its walkthrough, in a sentence or two each. The app adds the scope, the diffstat and the walkthrough format.").FontSize(11).TextColor(t.TextMuted).Grow(1)
			ui.Button(c, "Reset").OnClick(func() { f.questions = review.DefaultQuestions })
		})
	})
}

func (a *app) chooseRepo() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "Open a git repository", Directory: true})
		if err != nil || len(paths) == 0 {
			return
		}
		a.update(func() { a.openRepo(paths[0]) })
	}()
}

func (a *app) welcome(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(16).Padding(32).Background(t.Background).Children(func() {
		ui.Text(c, "AgentDiff").FontSize(30).Bold()
		ui.Text(c, "Review what your coding agent changed: an explanation first, then the diff, grouped, with comments you send back.").TextColor(t.TextMuted).MaxWidth(480).TextAlign(ui.Center)
		ui.PrimaryButton(c, "Open repository…").OnClick(a.chooseRepo)
		if a.repoErr != "" {
			ui.Text(c, a.repoErr).TextColor(t.Danger).MaxWidth(480).Selectable()
		}
		if len(a.cfg.Recent) > 0 {
			ui.Column(c).Gap(4).Width(420).Margin(12, 0, 0, 0).Children(func() {
				ui.Text(c, "Recent").FontSize(12).Bold().TextColor(t.TextMuted).Padding(0, 8)
				a.projectList(c, &a.projectNav, "", false, func(i int) { a.openRepo(a.cfg.Recent[i]) })
			})
		}
	})
}
