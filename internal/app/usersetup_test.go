package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestUserSetupSwitch(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1300, 1000)
	a.openSettings()
	a.settings.tab = agentTab
	tt.Frame()
	if a.cfg.Agent.UserSetup {
		t.Fatal("a new config loads the user's own setup")
	}
	if !tt.HasText("Claude Code runs in safe mode: without your CLAUDE.md files, skills, plugins, hooks and MCP servers. The Prompts tab holds the instructions it gets.") {
		t.Errorf("the tab does not say what Claude runs leave out: %q", tt.Texts())
	}

	a.settings.agent.provider = 1
	tt.Frame()
	snapshot(t, tt, "settings-user-setup")
	if !tt.HasText("Codex runs without your config.toml, hooks, plugins, apps and the AGENTS.md files of the repository, so a model set in config.toml does not apply. Codex still reads ~/.codex/AGENTS.md and your skills, because the CLI has no option to leave them out. The Prompts tab holds the instructions it gets.") {
		t.Errorf("the tab does not say what Codex runs leave out: %q", tt.Texts())
	}

	if err := tt.Click("Load my own instructions, skills, hooks and MCP servers"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.store.LoadConfig().Agent.UserSetup {
		t.Error("the switch was not saved")
	}
	if !tt.HasText("The agent gets your own setup, as in a run you start yourself. Your hooks run outside the read-only rules of the app.") {
		t.Errorf("the tab does not warn about the user's hooks: %q", tt.Texts())
	}
}
