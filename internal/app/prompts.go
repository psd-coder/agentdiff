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
		b, err := os.ReadFile(filepath.Join(root, file))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", file, err))
			continue
		}
		text := string(b)
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
func (a *app) prompts(root string) review.Prompts {
	repo, problems := readRepoPrompts(root)
	a.promptProblems = problems
	out := maps.Clone(a.cfg.Prompts)
	if out == nil {
		out = review.Prompts{}
	}
	maps.Copy(out, repo)
	return out
}
