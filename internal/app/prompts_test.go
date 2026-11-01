package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/review"
	"github.com/psd-coder/agentdiff/internal/store"
)

func TestRepoPromptFilesGoBeforeTheSettings(t *testing.T) {
	app := newTestApp(t)
	app.queue = make(chan func(), 64)
	root := app.repo.Root
	write(t, root, ".agentdiff/prompts/overview.md", "Only list the risks.\n{{format}}\n{{files}}\n")
	write(t, root, ".agentdiff/prompts/system.md", "Answer in Spanish.\n")
	write(t, root, ".agentdiff/prompts/deeper.md", " \n")
	write(t, root, ".agentdiff/prompts/followup.md", "{{files}}\n")
	app.cfg.Prompts = review.Prompts{
		review.PromptOverview: "From the settings.\n{{format}}\n{{files}}",
		review.PromptDeeper:   "Deeper from the settings.\n{{step}}\n{{format}}",
	}
	app.cfg.DeeperAhead = false
	fake := newFakeAgent(t, app)
	fake.answer("s1", "```json\n"+`{"summary": "s", "steps": [{"title": "Keys", "text": "k"}]}`+"\n```")

	app.generateOverview()
	pump(t, app, "the overview", func() bool { return !app.busy(store.TargetOverview) })
	req := fake.last()
	if !strings.Contains(req.Prompt, "Only list the risks.") || strings.Contains(req.Prompt, "From the settings.") {
		t.Errorf("overview prompt:\n%s", req.Prompt)
	}
	if req.SystemPrompt != "Answer in Spanish." {
		t.Errorf("system prompt = %q", req.SystemPrompt)
	}
	want := []string{".agentdiff/prompts/followup.md: The follow-up question prompt has no block {{files}}. The block {{format}} is missing. The block {{question}} is missing."}
	if !slices.Equal(app.promptProblems, want) {
		t.Errorf("problems = %q", app.promptProblems)
	}
	tt := ui.NewTester(app.view, 1300, 1000)
	tt.Frame()
	snapshot(t, tt, "overview-prompt-problem")
	if !tt.HasText(want[0]) {
		t.Errorf("the overview does not warn about the file: %q", tt.Texts())
	}

	app.deeper("", 0, "")
	walk := app.rev.Walks[0]
	pump(t, app, "the deeper walkthrough", func() bool { return !app.busy(walk.ID) })
	if prompt := fake.last().Prompt; !strings.Contains(prompt, "Deeper from the settings.") {
		t.Errorf("a blank deeper.md does not leave the prompt of the settings:\n%s", prompt)
	}
}

func TestPromptsTab(t *testing.T) {
	app := newTestApp(t)
	write(t, app.repo.Root, ".agentdiff/prompts/overview.md", "Repo.\n{{format}}\n{{files}}\n")
	tt := ui.NewTester(app.view, 1300, 1000)
	app.openSettings()
	app.settings.tab = promptsTab
	app.settings.prompts.picked = "Overview"
	tt.Frame()
	snapshot(t, tt, "settings-prompts")
	if !tt.HasText("This repository gives its own prompt in .agentdiff/prompts/overview.md, so its runs use that file. The text below goes to the runs in other repositories.") {
		t.Errorf("the tab does not say that the repository has its own overview prompt: %q", tt.Texts())
	}
	overview := review.TemplateOf(review.PromptOverview)
	form := &app.settings.prompts
	if form.texts[1] != overview.Default {
		t.Fatalf("the tab does not start with the default text:\n%s", form.texts[1])
	}

	form.texts[1] = "Only list the risks. {{files}}"
	tt.Frame()
	if !tt.HasText("Not saved. The block {{format}} is missing.") {
		t.Errorf("no problem shown: %q", tt.Texts())
	}
	if got := app.store.LoadConfig().Prompts; got != nil {
		t.Errorf("a prompt with a problem was saved: %q", got)
	}

	form.texts[1] = "Only list the risks. {{format}} {{files}}"
	tt.Frame()
	if got := app.store.LoadConfig().Prompts[review.PromptOverview]; got != "Only list the risks. {{format}} {{files}}" {
		t.Errorf("saved overview prompt = %q", got)
	}

	form.texts[1] = "Only list the risks. {{files}}"
	tt.Frame()
	if got := app.store.LoadConfig().Prompts[review.PromptOverview]; got != "Only list the risks. {{format}} {{files}}" {
		t.Errorf("a broken edit replaced the saved prompt with %q", got)
	}

	if err := tt.Click("Copy default"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != overview.Default {
		t.Errorf("clipboard = %q", tt.Clipboard())
	}
	if err := tt.Click("Reset"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := app.store.LoadConfig().Prompts; got != nil {
		t.Errorf("the default text stays in the config: %q", got)
	}
}

func TestPromptsTabShowsTheTextOfABlock(t *testing.T) {
	app := newTestApp(t)
	tt := ui.NewTester(app.view, 1300, 1000)
	app.openSettings()
	app.settings.tab = promptsTab
	app.settings.prompts.picked = "Overview"
	tt.Frame()
	if tt.HasText("- api/handler.go\n- cache/cache.go") {
		t.Fatal("the text of {{files}} shows before it is opened")
	}
	*app.settings.prompts.blocks["overview/files"] = true
	*app.settings.prompts.blocks["overview/format"] = true
	tt.Frame()
	snapshot(t, tt, "settings-prompt-blocks")
	for _, want := range []string{"- api/handler.go\n- cache/cache.go", "Answer with exactly one fenced JSON block and nothing else:"} {
		if !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.Contains(s, want) }) {
			t.Errorf("the open blocks do not show %q: %q", want, tt.Texts())
		}
	}
}

func TestSectionItemKeepsTheThreadOfItsChecklistQuestion(t *testing.T) {
	app := newTestApp(t)
	app.rev.Overview = &review.Overview{Agent: agent.Claude, Generated: time.Now(), Walk: &review.Walkthrough{
		Summary:  "s",
		Sections: []review.Section{{Title: "Checklist", Items: []review.Step{{Title: "What data changes?", Text: "None."}}}},
	}}
	th := app.rev.AddThread(review.Anchor{Kind: review.AnchorSection, Section: "Checklist › What data changes?"})
	th.Messages = []review.Message{{Role: review.RoleUser, Text: "Check the cache", Time: time.Now()}}
	tt := ui.NewTester(app.view, 1300, 1000)
	tt.Frame()
	snapshot(t, tt, "overview-sections")
	if !tt.HasText("Check the cache") {
		t.Errorf("the item does not show its thread: %q", tt.Texts())
	}
}
