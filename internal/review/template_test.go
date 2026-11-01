package review

import (
	"strings"
	"testing"

	"github.com/psd-coder/agentdiff/internal/agent"
	"github.com/psd-coder/agentdiff/internal/gitx"
)

func TestTemplateCheck(t *testing.T) {
	tests := []struct {
		name string
		tpl  PromptName
		text string
		want string
	}{
		{"accepts the required blocks with spaces inside the braces", PromptOverview, "Review. {{ format }}\n{{files}}", ""},
		{"accepts a prompt without its optional blocks", PromptFollowup, "{{question}} {{format}}", ""},
		{"names a block the prompt does not have once", PromptOverview, "{{format}} {{files}} {{step}} {{step}}", "The overview prompt has no block {{step}}."},
		{"names a missing required block", PromptDeeper, "{{format}}", "The block {{step}} is missing."},
		{"names every problem", PromptThread, "{{files}}", "The thread prompt has no block {{files}}. The block {{question}} is missing."},
		{"refuses any block in the system prompt", PromptSystem, "Be brief. {{scope}}", "The system prompt has no block {{scope}}."},
		{"leaves text that only looks like a block", PromptSystem, "Use {{ }} and {{1}} freely.", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ""
			if err := TemplateOf(tt.tpl).Check(tt.text); err != nil {
				got = err.Error()
			}
			if got != tt.want {
				t.Errorf("Check(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestDefaultTemplatesPassTheirCheck(t *testing.T) {
	for _, tpl := range Templates {
		if err := tpl.Check(tpl.Default); err != nil {
			t.Errorf("default %s prompt: %v", tpl.Name, err)
		}
	}
}

func TestPromptsFallBackToTheDefault(t *testing.T) {
	review := New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted})
	tests := []struct {
		name  string
		text  string
		want  string
		lacks []string
	}{
		{"uses a rewritten prompt", "Only list the risks.\n{{format}}\n{{files}}", "Only list the risks.", []string{"Add a section \"Checklist\""}},
		{"uses the default for a blank prompt", " \n", "Add a section \"Checklist\"", nil},
		{"uses the default for a prompt that misses a required block", "Only list the risks. {{files}}", "Add a section \"Checklist\"", []string{"Only list the risks."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompts := Prompts{PromptOverview: tt.text}
			prompt := OverviewExchange(review, testChanges, agent.Config{Provider: agent.Claude}, prompts).Request.Prompt
			checkPrompt(t, prompt, []string{tt.want, overviewSchema, "- a.go\n- b.go\n"}, tt.lacks)
		})
	}
}

func TestRewrittenSystemPromptGoesToEveryRun(t *testing.T) {
	review := New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted})
	prompts := Prompts{PromptSystem: "  Answer in Spanish.\n"}
	walk := review.AddWalk("", -1, "Why?")
	th := review.AddThread(Anchor{Kind: AnchorGeneral})
	cfg := agent.Config{Provider: agent.Claude}
	for name, req := range map[string]agent.Request{
		"overview":  OverviewExchange(review, testChanges, cfg, prompts).Request,
		"deeper":    DeeperExchange(review, walk, cfg, prompts, "").Request,
		"follow-up": FollowupExchange(review, walk, cfg, prompts, "", "And?").Request,
		"thread":    ThreadExchange(review, th, cfg, prompts, "", "Why?").Request,
	} {
		if req.SystemPrompt != "Answer in Spanish." {
			t.Errorf("%s system prompt = %q", name, req.SystemPrompt)
		}
	}
}

func TestRenderPutsValuesInOnce(t *testing.T) {
	review := New("/repo", gitx.Scope{Kind: gitx.ScopeUncommitted})
	review.Overview = &Overview{Walk: &Walkthrough{Steps: []Step{{Title: "Keys", Text: "Uses {{format}} literally."}}}, Sessions: Sessions{agent.Claude: "root"}}
	walk := review.AddWalk("", 0, "")
	prompts := Prompts{PromptDeeper: "{{context}}\n\n\n\n{{step}}\n\n{{question}}\n\n{{format}}"}
	prompt := DeeperExchange(review, walk, agent.Config{Provider: agent.Claude}, prompts, "").Request.Prompt
	if !strings.HasPrefix(prompt, "Go one level deeper into this step") {
		t.Errorf("the empty context leaves a gap at the start: %q", prompt[:min(len(prompt), 60)])
	}
	if strings.Contains(prompt, "\n\n\n") {
		t.Errorf("the prompt has more than one blank line in a row:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Uses {{format}} literally.") {
		t.Errorf("the step text lost its braces:\n%s", prompt)
	}
	if strings.Count(prompt, "Answer with exactly one fenced JSON block") != 1 {
		t.Errorf("the format block is not in the prompt exactly once:\n%s", prompt)
	}
}
