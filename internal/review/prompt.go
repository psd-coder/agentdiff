package review

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// defaultQuestions are the checklist the default overview answers in a
// section after its steps.
const defaultQuestions = `1. What data changes: schemas, storage, formats?
2. What UI changes, and how do I see them?
3. What architecture and API changes?
4. Which changes are not beautiful?
5. Which assumptions and rules force the non-beautiful changes?`

const systemPrompt = `You help a developer review code changes that a coding agent made. You only read: never edit files, never run commands that change the repository. Inspect changes with read-only git commands (git diff, git show, git log, git blame, git status) and with the Read, Grep and Glob tools. Other shell commands are refused, so do not try them; simple pipes into cat -n, grep, head, tail or wc are fine. Be concise and concrete.`

const refSchema = `{"file": "path/to/file", "side": "new", "start": 10, "end": 18, "why": "..."}`

const walkSchema = `{"summary": "...", "steps": [{"title": "...", "text": "...", "refs": [` + refSchema + `]}], "sections": [{"title": "...", "items": [{"title": "...", "text": "...", "refs": []}]}]}`

const overviewSchema = `{"summary": "...", "steps": [{"title": "...", "text": "...", "refs": [` + refSchema + `]}], "sections": [{"title": "...", "items": [{"title": "...", "text": "...", "refs": []}]}], "groups": [{"title": "short title", "summary": "one sentence", "files": ["path/one", "path/two"]}]}`

// walkStyle says how long a walkthrough and its parts are.
const walkStyle = `Write a walkthrough that a reviewer reads in two minutes:
- The summary has 1 to 3 short sentences. Say what changed and why.
- Write 3 to 7 steps, in the order the reviewer should read the code. A step title has at most 8 words. A step text has at most 3 short sentences.
- Give each step 0 to 2 references, to the lines that show the step best.

Explain the idea and leave the details out. The reviewer asks for a deeper walkthrough of any step that needs one.`

// DefaultOverview is the default overview prompt with questions as its
// checklist.
func DefaultOverview(questions string) string {
	return "The change set to review is {{scope}}\n\nDiffstat:\n```\n{{diffstat}}\n```\n\nRead the actual changes before you answer.\n\n" +
		walkStyle + "\n\n" +
		"Add a section \"Checklist\" with one item for each of these questions. Copy the question into the item title, and answer it in 1 or 2 sentences with at most 1 reference. Answer \"None.\" when the question does not apply.\n\n" +
		strings.TrimSpace(questions) + "\n\n" +
		"Group the changed files by intent, in the order a reviewer should read them (for example: core logic first, then its callers, then tests and config). Put every changed file in exactly one group.\n\n" +
		"{{format}}\n\nThe changed files are:\n{{files}}\n"
}

const defaultDeeper = "{{context}}\n\n{{step}}\n\n{{question}}\n\n" +
	"Answer with a new walkthrough one level lower than the one above: smaller steps, closer to the code. References may also point to files that did not change, when the reviewer needs them to follow the code. Do not repeat what the walkthrough above already says. When the question has a short answer, give it in the summary and use fewer steps.\n\n" +
	walkStyle + "\n\n{{format}}\n"

const defaultFollowup = "{{context}}\n\n{{question}}\n\n" +
	"Answer with a walkthrough. When the question has a short answer, give it in the summary and use fewer steps, or none.\n\n" +
	walkStyle + "\n\n{{format}}\n"

const defaultThread = "{{context}}\n\nThis is a new, separate discussion thread. {{anchor}}\n\n{{history}}\n\n{{question}}\n"

// FormatText is the {{format}} block of a prompt: the JSON the app reads
// and the rules for line references. newRev is the revision that holds
// the reviewed version of the files, empty for the working tree.
func FormatText(name PromptName, newRev string) string {
	read := "from a read of the file in the working tree, which shows line numbers"
	if newRev != "" {
		read = fmt.Sprintf("from `git show %s:<path> | cat -n`, which numbers the lines of the reviewed version", newRev)
	}
	schema := walkSchema
	if name == PromptOverview {
		schema = overviewSchema
	}
	var b strings.Builder
	b.WriteString("Answer with exactly one fenced JSON block and nothing else:\n\n```json\n" + schema + "\n```\n\n")
	b.WriteString(`- "summary": the answer in short. Inline Markdown such as ` + "`code`" + ` is fine.
- "steps": the steps in the order the reviewer reads them. A "title" is plain text. A "text" may hold inline Markdown, but no code blocks and no headings.
- "sections": the other parts of the answer that the instructions ask for, shown after the steps in this order. A section has a "title" and "items". An item has a "title", a "text" and "refs", written as a step is. Leave "sections" out when the instructions ask for no other parts.
- "refs": the lines that show a step or an item best, at most 15 lines each. "side" is "new" for lines of the reviewed version and "old" for removed lines. "start" and "end" are 1-based line numbers on that side. Take them from the hunk headers of git diff (` + "`@@ -old +new @@`" + `) or ` + read + `. Check them: the app shows exactly these lines next to your text. "why" says in at most 10 words what to look at.
`)
	if name == PromptOverview {
		b.WriteString(`- "groups": the changed files in groups, in the order the reviewer reads them. A "title" is short, a "summary" is one sentence, and "files" holds the paths as the list of changed files writes them.
`)
	}
	return b.String()
}

// FileList is the {{files}} block: the changed files, one per line.
func FileList(files []string) string {
	var b strings.Builder
	for _, f := range files {
		b.WriteString("- " + f + "\n")
	}
	return b.String()
}

// overviewPrompt asks for the walkthrough of the whole change set, its
// sections and the files grouped by intent, all in one JSON block that
// readOverview takes apart.
func overviewPrompt(p Prompts, scope, diffstat, newRev string, files []string) string {
	return p.render(PromptOverview, map[string]string{
		"scope":    scope,
		"diffstat": diffstat,
		"files":    FileList(files),
		"format":   FormatText(PromptOverview, newRev),
	})
}

// deeperRequest is what a deeper walkthrough is about.
type deeperRequest struct {
	// Step is the step to explain, nil for the parent walkthrough as a
	// whole.
	Step     *Step
	Question string
	// Known is true when the session already holds the walkthroughs
	// above, as a fork of the parent's session does.
	Known bool
	// Context and Path start a session that does not know them: the
	// change set, and what the walkthroughs above were about.
	Context string
	Path    []string
	NewRev  string
}

func deeperPrompt(p Prompts, d deeperRequest) string {
	values := map[string]string{"format": FormatText(PromptDeeper, d.NewRev)}
	if !d.Known {
		var b strings.Builder
		b.WriteString(d.Context)
		b.WriteString("The reviewer reads a walkthrough of these changes")
		if len(d.Path) > 0 {
			b.WriteString(" and went deeper into: " + strings.Join(d.Path, " > "))
		}
		b.WriteString(".")
		values["context"] = b.String()
	}
	if s := d.Step; s != nil {
		var b strings.Builder
		b.WriteString("Go one level deeper into this step of the walkthrough:\n\n")
		fmt.Fprintf(&b, "### %s\n%s\n", s.Title, s.Text)
		for _, r := range s.Refs {
			fmt.Fprintf(&b, "- %s: %s\n", r.Label(), r.Why)
		}
		values["step"] = b.String()
	}
	values["question"] = "The reviewer wants to know how it works in more detail."
	if d.Question != "" {
		values["question"] = "The reviewer asks: " + d.Question
	}
	return p.render(PromptDeeper, values)
}

// followupRequest is a follow-up question in a question's conversation.
type followupRequest struct {
	Question string
	// Known is true when the session holds the conversation so far.
	Known bool
	// Context and Earlier start a session that does not: the change set,
	// and the questions and answers before this one.
	Context string
	Earlier []Turn
	NewRev  string
}

func followupPrompt(p Prompts, f followupRequest) string {
	values := map[string]string{
		"format":   FormatText(PromptFollowup, f.NewRev),
		"question": "The reviewer asks a follow-up question: " + f.Question,
	}
	if !f.Known {
		var b strings.Builder
		b.WriteString(f.Context)
		b.WriteString("The reviewer asked about these changes before:\n\n")
		for _, t := range f.Earlier {
			fmt.Fprintf(&b, "Q: %s\n", t.Question)
			if t.Content != nil {
				fmt.Fprintf(&b, "A: %s\n", t.Content.Summary)
			}
			b.WriteString("\n")
		}
		values["context"] = b.String()
	}
	return p.render(PromptFollowup, values)
}

// readOverview turns the agent's answer into an overview: a walkthrough
// when the answer holds one, else the Markdown and its groups block.
func readOverview(answer string, files []string) Overview {
	if w, ok := parseWalkthrough(answer); ok {
		groups := NormalizeGroups(w.Groups, files)
		w.Groups = nil
		return Overview{Walk: w, Groups: groups}
	}
	body, groups := ParseOverview(answer, files)
	return Overview{Markdown: body, Groups: groups}
}

var groupsBlock = regexp.MustCompile("(?s)```json\\s*(\\{.*?\"groups\".*?\\})\\s*```")

// ParseOverview takes the groups block out of an overview. Files the
// agent left out, or named wrongly, go to groups by directory, so every
// changed file is in exactly one group.
func ParseOverview(md string, files []string) (string, []Group) {
	var groups []Group
	body := md
	if m := groupsBlock.FindAllStringSubmatchIndex(md, -1); len(m) > 0 {
		last := m[len(m)-1]
		var parsed struct {
			Groups []Group `json:"groups"`
		}
		if json.Unmarshal([]byte(md[last[2]:last[3]]), &parsed) == nil {
			groups = parsed.Groups
			body = strings.TrimSpace(md[:last[0]] + md[last[1]:])
		}
	}
	return body, NormalizeGroups(groups, files)
}

func NormalizeGroups(groups []Group, files []string) []Group {
	known := map[string]bool{}
	for _, f := range files {
		known[f] = true
	}
	seen := map[string]bool{}
	var out []Group
	for _, g := range groups {
		var kept []string
		for _, f := range g.Files {
			f = strings.TrimPrefix(strings.TrimSpace(f), "./")
			if known[f] && !seen[f] {
				seen[f] = true
				kept = append(kept, f)
			}
		}
		if len(kept) > 0 {
			g.Files = kept
			out = append(out, g)
		}
	}
	var rest []string
	for _, f := range files {
		if !seen[f] {
			rest = append(rest, f)
		}
	}
	if len(rest) == 0 {
		return out
	}
	if len(out) > 0 {
		return append(out, Group{Title: "Other changes", Files: rest})
	}
	return append(out, DirectoryGroups(rest)...)
}

// DirectoryGroups groups files by their directory, for a review without
// an overview yet.
func DirectoryGroups(files []string) []Group {
	byDir := map[string][]string{}
	var dirs []string
	for _, f := range files {
		d := path.Dir(f)
		if d == "." {
			d = "Repository root"
		}
		if _, ok := byDir[d]; !ok {
			dirs = append(dirs, d)
		}
		byDir[d] = append(byDir[d], f)
	}
	slices.Sort(dirs)
	out := make([]Group, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, Group{Title: d, Files: byDir[d]})
	}
	return out
}

// MarkdownSection is a part of an overview written as Markdown.
type MarkdownSection struct {
	Title string
	Body  string
}

// Sections splits an overview at its level-2 headings. Text before the
// first heading is a section without a title.
func Sections(md string) []MarkdownSection {
	var out []MarkdownSection
	title, start := "", 0
	inFence := false
	// The body is a slice of md: the overview is drawn every frame, and
	// adding to the body line by line copies it once per line.
	flush := func(end int) {
		body := strings.TrimSpace(md[start:end])
		if title != "" || body != "" {
			out = append(out, MarkdownSection{Title: title, Body: body})
		}
	}
	pos := 0
	for line := range strings.SplitSeq(md, "\n") {
		next := min(pos+len(line)+1, len(md))
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(line, "## ") {
			flush(pos)
			title, start = strings.TrimSpace(strings.TrimPrefix(line, "## ")), next
		}
		pos = next
	}
	flush(len(md))
	return out
}

// contextPrompt starts a conversation when there is no overview to go on
// from.
func contextPrompt(scope, diffstat string) string {
	return fmt.Sprintf("The change set under review is %s\n\nDiffstat:\n```\n%s\n```\n\nRead the changes you need before you answer.\n\n", scope, strings.TrimSpace(diffstat))
}

// threadPrompt starts the session of a thread, a new or a forked one,
// with what the thread is about and what was said in it so far. context
// is empty for a session that forks the overview's.
func threadPrompt(p Prompts, t *Thread, text, context string) string {
	var anchor strings.Builder
	switch t.Anchor.Kind {
	case AnchorLines:
		fmt.Fprintf(&anchor, "It is about %s.\n", t.Anchor.Label())
		if t.Anchor.Quote != "" {
			fmt.Fprintf(&anchor, "\n```\n%s\n```\n", t.Anchor.Quote)
		}
	case AnchorFile:
		fmt.Fprintf(&anchor, "It is about the changes in the file %s.\n", t.Anchor.File)
	case AnchorSection:
		fmt.Fprintf(&anchor, "It is about this part of your overview: %q.\n", t.Anchor.Section)
		if t.Anchor.Quote != "" {
			fmt.Fprintf(&anchor, "\n%s\n", quote(t.Anchor.Quote))
		}
	default:
		anchor.WriteString("It is about the change set as a whole.\n")
	}
	values := map[string]string{
		"context":  context,
		"anchor":   anchor.String(),
		"question": "The reviewer asks:\n\n" + text,
	}
	if earlier := history(t); earlier != "" {
		values["history"] = "The thread so far:\n\n" + earlier
	}
	return p.render(PromptThread, values)
}

// history is the thread's messages before the last one, which is the
// question being sent.
func history(t *Thread) string {
	if len(t.Messages) < 2 {
		return ""
	}
	var b strings.Builder
	for _, m := range t.Messages[:len(t.Messages)-1] {
		if strings.TrimSpace(m.Text) == "" {
			continue
		}
		who := "Reviewer"
		if m.Role == RoleAgent {
			who = "Agent"
		}
		fmt.Fprintf(&b, "%s: %s\n\n", who, strings.TrimSpace(m.Text))
	}
	return b.String()
}

func quote(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}
