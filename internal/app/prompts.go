package app

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/psd-coder/agentdiff/internal/review"
)

// repoPromptDir is where a repository keeps the prompts it gives its
// reviews in place of the ones in the settings.
const repoPromptDir = ".agentdiff/prompts"

func repoPromptFile(name review.PromptName) string {
	return filepath.Join(repoPromptDir, string(name)+".md")
}

// readRepoPrompts reads the prompt files of the repository at root. A
// file that is missing or blank gives no prompt. A file the app cannot
// read or use gives a problem that names it.
func readRepoPrompts(root string) (review.Prompts, []string) {
	prompts := review.Prompts{}
	var problems []string
	for _, t := range review.Templates {
		file := repoPromptFile(t.Name)
		data, err := os.ReadFile(filepath.Join(root, file))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", file, err))
			continue
		}
		text := string(data)
		if strings.TrimSpace(text) == "" {
			continue
		}
		if err := t.Check(text); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", file, err))
			continue
		}
		prompts[t.Name] = text
	}
	return prompts, problems
}

// prompts are the prompts of a run in the repository at root: its own
// prompt files over the prompts in the settings. It keeps the problems of
// the files for the overview page.
func (app *app) prompts(root string) review.Prompts {
	repo, problems := readRepoPrompts(root)
	app.repoPrompts, app.promptProblems = repo, problems
	out := maps.Clone(app.cfg.Prompts)
	if out == nil {
		out = review.Prompts{}
	}
	maps.Copy(out, repo)
	return out
}

// promptSource is where the prompts of the last run took the prompt from:
// a file of the repository, the settings or the default text.
func (app *app) promptSource(name review.PromptName) review.PromptSource {
	switch {
	case app.repoPrompts.Has(name):
		return review.SourceRepo
	case app.cfg.Prompts.Has(name):
		return review.SourceSettings
	}
	return review.SourceDefault
}

// promptFrom says which prompt an overview was written with. It is empty
// for an overview written before the app saved it.
func promptFrom(source review.PromptSource) string {
	switch source {
	case review.SourceRepo:
		return "with the prompt in " + repoPromptFile(review.PromptOverview)
	case review.SourceSettings:
		return "with the prompt from the settings"
	case review.SourceDefault:
		return "with the default prompt"
	}
	return ""
}

// touchesPrompts reports whether a change in the working tree under dir,
// as the watch hints it, can change the prompt files of the repository.
func touchesPrompts(dir string) bool {
	return dir == "." || dir == repoPromptDir || strings.HasPrefix(repoPromptDir, dir+"/")
}
