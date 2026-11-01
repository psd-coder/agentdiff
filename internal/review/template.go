package review

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// PromptName names a prompt the app sends. It is also the key of the
// prompt in the config and the name of its file in a repository.
type PromptName string

const (
	PromptSystem   PromptName = "system"
	PromptOverview PromptName = "overview"
	PromptDeeper   PromptName = "deeper"
	PromptFollowup PromptName = "followup"
	PromptThread   PromptName = "thread"
)

// Block is a part of a prompt that the app writes, put into a template as
// {{name}}.
type Block struct {
	Name     string
	Required bool
	About    string
}

// Template is a prompt the user can rewrite: its blocks and the text the
// app sends when the user did not rewrite it.
type Template struct {
	Name    PromptName
	Title   string
	About   string
	Blocks  []Block
	Default string
}

var formatBlock = Block{Name: "format", Required: true, About: "How to write the answer so that the app can read it: the JSON fields and the rules for line references."}

// Templates are the prompts in the order the settings list them.
var Templates = []Template{
	{
		Name:    PromptSystem,
		Title:   "System",
		About:   "The system prompt of every run. The app keeps the agent read-only whatever this text says.",
		Default: systemPrompt,
	},
	{
		Name:  PromptOverview,
		Title: "Overview",
		About: "Writes the overview of the changes: the walkthrough, its sections and the file groups.",
		Blocks: []Block{
			formatBlock,
			{Name: "files", Required: true, About: "The changed files, one per line."},
			{Name: "scope", About: "What is reviewed, as the uncommitted changes or one commit, with the git command that shows it."},
			{Name: "diffstat", About: "The output of git diff --stat."},
		},
		Default: DefaultOverview(defaultQuestions),
	},
	{
		Name:  PromptDeeper,
		Title: "Deeper walkthrough",
		About: "Writes the walkthrough of a step one level deeper, or answers a question about a step or a page.",
		Blocks: []Block{
			formatBlock,
			{Name: "step", Required: true, About: "The step to go deeper into, with its text and references. It is empty for a question about the page as a whole."},
			{Name: "question", About: "The reviewer's question, or a request for more detail when there is none."},
			{Name: "context", About: "The changes and the walkthroughs above, for an agent session that does not know them. It is empty when the session goes on from the walkthrough above."},
		},
		Default: defaultDeeper,
	},
	{
		Name:  PromptFollowup,
		Title: "Follow-up question",
		About: "Answers a follow-up question in the conversation of a question.",
		Blocks: []Block{
			formatBlock,
			{Name: "question", Required: true, About: "The follow-up question."},
			{Name: "context", About: "The changes and the questions and answers before, for an agent session that does not know them. It is empty when the session goes on from the conversation."},
		},
		Default: defaultFollowup,
	},
	{
		Name:  PromptThread,
		Title: "Thread",
		About: "Starts the agent session of a discussion thread. The later messages of the thread go to that session as the reviewer wrote them.",
		Blocks: []Block{
			{Name: "question", Required: true, About: "The reviewer's message."},
			{Name: "anchor", About: "What the thread is about: lines with their code, a file, a part of the walkthrough, or the changes as a whole."},
			{Name: "history", About: "The earlier messages of the thread. It is empty for its first message."},
			{Name: "context", About: "The changes, for an agent session that starts without the session of the overview. It is empty when the thread forks that session."},
		},
		Default: defaultThread,
	},
}

// TemplateOf returns the template of a prompt.
func TemplateOf(name PromptName) Template {
	i := slices.IndexFunc(Templates, func(t Template) bool { return t.Name == name })
	return Templates[i]
}

var blockRef = regexp.MustCompile(`\{\{\s*([A-Za-z_]+)\s*\}\}`)

// Check reports what keeps text from working as the prompt: blocks the
// prompt does not have and required blocks that text leaves out.
func (t Template) Check(text string) error {
	used := map[string]bool{}
	var problems []string
	for _, m := range blockRef.FindAllStringSubmatch(text, -1) {
		name := m[1]
		if used[name] {
			continue
		}
		used[name] = true
		if !slices.ContainsFunc(t.Blocks, func(b Block) bool { return b.Name == name }) {
			problems = append(problems, fmt.Sprintf("The %s prompt has no block {{%s}}.", strings.ToLower(t.Title), name))
		}
	}
	for _, b := range t.Blocks {
		if b.Required && !used[b.Name] {
			problems = append(problems, fmt.Sprintf("The block {{%s}} is missing.", b.Name))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, " "))
	}
	return nil
}

// Prompts are the texts of the prompts that the user rewrote, by name.
// A prompt that is missing, blank or broken gets its default text.
type Prompts map[PromptName]string

func (p Prompts) text(name PromptName) string {
	t := TemplateOf(name)
	if text := p[name]; strings.TrimSpace(text) != "" && t.Check(text) == nil {
		return text
	}
	return t.Default
}

var blankLines = regexp.MustCompile(`\n{3,}`)

// render puts the values of the blocks into the prompt in one pass, so a
// value that holds {{name}} stays as it is. A block that is empty leaves
// no gap: more than one blank line becomes one.
func (p Prompts) render(name PromptName, values map[string]string) string {
	out := blockRef.ReplaceAllStringFunc(p.text(name), func(ref string) string {
		return strings.TrimSpace(values[blockRef.FindStringSubmatch(ref)[1]])
	})
	return strings.TrimSpace(blankLines.ReplaceAllString(out, "\n\n")) + "\n"
}

// System is the system prompt of every run.
func (p Prompts) System() string { return strings.TrimSpace(p.text(PromptSystem)) }
