package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/agent"
)

func TestUserSetupCheckbox(t *testing.T) {
	app := newTestApp(t)
	app.openSettings()
	app.settings.tab = agentTab
	tt := ui.NewTester(app.settingsView, settingsWidth, settingsMaxHeight)
	if app.cfg.Agent().UserSetup {
		t.Fatal("a new config loads the user's own setup")
	}
	if !tt.HasText("Claude Code runs in safe mode: without your CLAUDE.md files, skills, plugins, hooks and MCP servers. The Prompts tab holds the instructions it gets.") {
		t.Errorf("the tab does not say what Claude runs leave out: %q", tt.Texts())
	}

	app.settings.provider = 1
	tt.Frame()
	snapshot(t, tt, "settings-user-setup")
	if !tt.HasText("Codex runs without your config.toml, hooks, plugins, apps and the AGENTS.md files of the repository, so a model set in config.toml does not apply. Codex still reads ~/.codex/AGENTS.md and your skills, because the CLI has no option to leave them out. The Prompts tab holds the instructions it gets.") {
		t.Errorf("the tab does not say what Codex runs leave out: %q", tt.Texts())
	}

	if err := tt.Click("Load my own instructions, skills, hooks and MCP servers"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !app.store.LoadConfig().Agent().UserSetup {
		t.Error("the check box was not saved")
	}
	if !tt.HasText("The agent gets your own setup, as in a run you start yourself. Your hooks run outside the read-only rules of the app.") {
		t.Errorf("the tab does not warn about the user's hooks: %q", tt.Texts())
	}
}

func TestModelFieldShowsHowOpenCodeNamesModels(t *testing.T) {
	app := newTestApp(t)
	app.openSettings()
	app.settings.tab = agentTab
	tt := ui.NewTester(app.settingsView, settingsWidth, settingsMaxHeight)
	const help = "OpenCode takes the provider and the model."
	if tt.HasText(help) {
		t.Errorf("Claude Code shows the model format of OpenCode: %q", tt.Texts())
	}

	app.settings.provider = 2
	tt.Frame()
	snapshot(t, tt, "settings-opencode")
	if !tt.HasText(help) {
		t.Errorf("OpenCode does not show its model format: %q", tt.Texts())
	}
}

func TestEachAgentKeepsItsModel(t *testing.T) {
	app := newTestApp(t)
	app.openSettings()
	app.settings.tab = agentTab
	tt := ui.NewTester(app.settingsView, settingsWidth, settingsMaxHeight)
	typeModel := func(model string) {
		t.Helper()
		if err := tt.Click("Model"); err != nil {
			t.Fatal(err)
		}
		tt.Type(model)
		tt.Frame()
	}
	choose := func(name string) {
		t.Helper()
		if err := tt.Click(name); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		tt.Frame()
	}
	typeModel("opus")
	choose("OpenCode")
	if got := app.settings.agent.model; got != "" {
		t.Errorf("OpenCode shows the model %q", got)
	}
	typeModel("opencode/glm")
	choose("Claude Code")
	if got := app.settings.agent.model; got != "opus" {
		t.Errorf("Claude Code shows the model %q, want opus", got)
	}

	got := app.store.LoadConfig()
	if got.Provider != agent.Claude || got.Agent().Model != "opus" || got.AgentOf(agent.OpenCode).Model != "opencode/glm" {
		t.Errorf("saved %+v", got)
	}
}

func TestAgentLabelOpensAgentSettings(t *testing.T) {
	app := newTestApp(t)
	app.cfg.SetAgent(agent.Config{Provider: agent.OpenCode, Model: "opencode/glm"})
	tt := ui.NewTester(app.view, 1300, 800)
	if err := tt.Click("OpenCode · opencode/glm | default"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if app.settings == nil || app.settings.tab != agentTab || providers[app.settings.provider] != agent.OpenCode {
		t.Errorf("settings = %+v, want the Agent tab on OpenCode", app.settings)
	}
}

func TestAgentLabel(t *testing.T) {
	cases := []struct {
		name  string
		cfg   agent.Config
		label string
		tip   string
	}{
		{
			"nothing set", agent.Config{Provider: agent.Claude},
			"Claude Code · default | default", "Agent settings",
		},
		{
			"automatic runs differ", agent.Config{Provider: agent.OpenCode, Model: "opencode/glm", Effort: "high", AutoEffort: "low"},
			"OpenCode · opencode/glm | high", "Automatic runs use model: opencode/glm, effort: low\nAgent settings",
		},
		{
			"automatic model only", agent.Config{Provider: agent.Codex, AutoModel: "gpt-5-mini"},
			"Codex · default | default", "Automatic runs use model: gpt-5-mini, effort: default\nAgent settings",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if label, tip := agentLabel(c.cfg); label != c.label || tip != c.tip {
				t.Errorf("agentLabel = %q, %q, want %q, %q", label, tip, c.label, c.tip)
			}
		})
	}
}

func TestCommandHelpWarnsWhenTheSandboxDoesNotReach(t *testing.T) {
	const warning = "The macOS sandbox does not reach into the container"
	tests := []struct {
		name     string
		provider agent.Provider
		command  []string
		warns    bool
	}{
		{"OpenCode on the host", agent.OpenCode, []string{"/opt/bin/opencode"}, false},
		{"OpenCode in a container", agent.OpenCode, []string{"dev", "opencode"}, true},
		{"Claude Code in a container", agent.Claude, []string{"dev", "claude"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := strings.Contains(commandHelp(tt.provider, tt.command), warning); got != tt.warns {
				t.Errorf("warns = %v, want %v", got, tt.warns)
			}
		})
	}
}

func TestCommandFieldKeepsQuotedWords(t *testing.T) {
	app := newTestApp(t)
	app.openSettings()
	app.settings.tab = agentTab
	tt := ui.NewTester(app.settingsView, settingsWidth, settingsMaxHeight)
	app.settings.agent.command = `docker exec -i "my box" claude`
	tt.Frame()
	want := []string{"docker", "exec", "-i", "my box", "claude"}
	if got := app.store.LoadConfig().Agent().Command; !slices.Equal(got, want) {
		t.Errorf("command = %q, want %q", got, want)
	}
	app.closeSettings()
	app.openSettings()
	if got := app.settings.agent.command; got != `docker exec -i 'my box' claude` {
		t.Errorf("settings opened again with %q", got)
	}
}
