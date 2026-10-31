package review

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// DefaultQuestions are the checklist the overview answers under its
// walkthrough.
const DefaultQuestions = `1. What data changes: schemas, storage, formats?
2. What UI changes, and how do I see them?
3. What architecture and API changes?
4. Which changes are not beautiful?
5. Which assumptions and rules force the non-beautiful changes?`

// LegacyQuestions were the default before the checklist. A configuration
// that still holds them gets DefaultQuestions instead.
const LegacyQuestions = `Explain the changes:

1. Explain on high level.
2. Explain data changes.
3. Explain UI changes and how to see them.
4. Explain architecture and API changes.
5. Explain non-beautiful changes.
6. Tell about assumptions and rules which force non-beautiful changes.`

const SystemPrompt = `You help a developer review code changes that a coding agent made. You only read: never edit files, never run commands that change the repository. Inspect changes with read-only git commands (git diff, git show, git log, git blame, git status) and with the Read, Grep and Glob tools. Other shell commands are refused, so do not try them; simple pipes into cat -n, grep, head, tail or wc are fine. Be concise and concrete.`

const walkSchema = `{"summary": "...", "steps": [{"title": "...", "text": "...", "refs": [{"file": "path/to/file", "side": "new", "start": 10, "end": 18, "why": "..."}]}]}`

const overviewSchema = `{"summary": "...", "steps": [{"title": "...", "text": "...", "refs": [{"file": "path/to/file", "side": "new", "start": 10, "end": 18, "why": "..."}]}], "checklist": [{"question": "...", "answer": "...", "refs": []}], "groups": [{"title": "short title", "summary": "one sentence", "files": ["path/one", "path/two"]}]}`

// walkRules says how to write a walkthrough. newRev is the revision that
// holds the reviewed version of the files, empty for the working tree.
func walkRules(newRev string) string {
	read := "from a read of the file in the working tree, which shows line numbers"
	if newRev != "" {
		read = fmt.Sprintf("from `git show %s:<path> | cat -n`, which numbers the lines of the reviewed version", newRev)
	}
	return `Write a walkthrough that a reviewer reads in two minutes:
- "summary": 1 to 3 short sentences. Say what changed and why.
- "steps": 3 to 7 steps, in the order the reviewer should read the code. A "title" has at most 8 words. A "text" has at most 3 short sentences. Inline Markdown such as ` + "`code`" + ` is fine, code blocks and headings are not.
- "refs": 0 to 2 references per step, to the lines that show the step best, at most 15 lines each. "side" is "new" for lines of the reviewed version and "old" for removed lines. "start" and "end" are 1-based line numbers on that side. Take them from the hunk headers of git diff (` + "`@@ -old +new @@`" + `) or ` + read + `. Check them: the app shows exactly these lines next to your text. "why" says in at most 10 words what to look at.

Explain the idea and leave the details out. The reviewer asks for a deeper walkthrough of any step that needs one.
`
}

// OverviewPrompt asks for the walkthrough of the whole change set, the
// answers to the checklist, and the files grouped by intent, all in one
// JSON block that ReadOverview takes apart.
func OverviewPrompt(scope, diffstat, newRev string, files []string, questions string) string {
	if strings.TrimSpace(questions) == "" {
		questions = DefaultQuestions
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The change set to review is %s\n\n", scope)
	fmt.Fprintf(&b, "Diffstat:\n```\n%s\n```\n\n", strings.TrimSpace(diffstat))
	b.WriteString("Read the actual changes before you answer.\n\n")
	b.WriteString(walkRules(newRev))
	b.WriteString("\nAlso fill in:\n")
	b.WriteString("- \"checklist\": answer each of these questions in 1 or 2 sentences, with at most 1 reference. Copy the question into \"question\". Answer \"None.\" when it does not apply.\n\n")
	b.WriteString(strings.TrimSpace(questions))
	b.WriteString("\n\n- \"groups\": the changed files grouped by intent, in the order a reviewer should read them (for example: core logic first, then its callers, then tests and config). Put every changed file in exactly one group.\n\n")
	b.WriteString("Answer with exactly one fenced JSON block and nothing else:\n\n```json\n" + overviewSchema + "\n```\n\n")
	b.WriteString("The changed files are:\n")
	for _, f := range files {
		b.WriteString("- " + f + "\n")
	}
	return b.String()
}

// DeeperRequest is what a deeper walkthrough is about.
type DeeperRequest struct {
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

func DeeperPrompt(d DeeperRequest) string {
	var b strings.Builder
	if !d.Known {
		b.WriteString(d.Context)
		b.WriteString("The reviewer reads a walkthrough of these changes")
		if len(d.Path) > 0 {
			b.WriteString(" and went deeper into: " + strings.Join(d.Path, " > "))
		}
		b.WriteString(".\n\n")
	}
	if s := d.Step; s != nil {
		b.WriteString("Go one level deeper into this step of the walkthrough:\n\n")
		fmt.Fprintf(&b, "### %s\n%s\n", s.Title, s.Text)
		for _, r := range s.Refs {
			fmt.Fprintf(&b, "- %s: %s\n", r.Label(), r.Why)
		}
		b.WriteString("\n")
	}
	if d.Question != "" {
		fmt.Fprintf(&b, "The reviewer asks: %s\n\n", d.Question)
	} else {
		b.WriteString("The reviewer wants to know how it works in more detail.\n\n")
	}
	b.WriteString("Answer with a new walkthrough one level lower than the one above: smaller steps, closer to the code. References may also point to files that did not change, when the reviewer needs them to follow the code. Do not repeat what the walkthrough above already says. When the question has a short answer, give it in the summary and use fewer steps.\n\n")
	b.WriteString(walkRules(d.NewRev))
	b.WriteString("\nAnswer with exactly one fenced JSON block and nothing else:\n\n```json\n" + walkSchema + "\n```\n")
	return b.String()
}

// FollowupRequest is a follow-up question in a question's conversation.
type FollowupRequest struct {
	Question string
	// Known is true when the session holds the conversation so far.
	Known bool
	// Context and Earlier start a session that does not: the change set,
	// and the questions and answers before this one.
	Context string
	Earlier []Turn
	NewRev  string
}

func FollowupPrompt(f FollowupRequest) string {
	var b strings.Builder
	if !f.Known {
		b.WriteString(f.Context)
		b.WriteString("The reviewer asked about these changes before:\n\n")
		for _, t := range f.Earlier {
			fmt.Fprintf(&b, "Q: %s\n", t.Question)
			if t.Content != nil {
				fmt.Fprintf(&b, "A: %s\n", t.Content.Summary)
			}
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "The reviewer asks a follow-up question: %s\n\n", f.Question)
	b.WriteString("Answer with a walkthrough. When the question has a short answer, give it in the summary and use fewer steps, or none.\n\n")
	b.WriteString(walkRules(f.NewRev))
	b.WriteString("\nAnswer with exactly one fenced JSON block and nothing else:\n\n```json\n" + walkSchema + "\n```\n")
	return b.String()
}

// ReadOverview turns the agent's answer into an overview: a walkthrough
// when the answer holds one, else the Markdown and its groups block.
func ReadOverview(answer string, files []string) Overview {
	if w, ok := ParseWalkthrough(answer); ok {
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

type Section struct {
	Title string
	Body  string
}

// Sections splits an overview at its level-2 headings. Text before the
// first heading is a section without a title.
func Sections(md string) []Section {
	var out []Section
	title, start := "", 0
	inFence := false
	// The body is a slice of md: the overview is drawn every frame, and
	// adding to the body line by line copies it once per line.
	flush := func(end int) {
		body := strings.TrimSpace(md[start:end])
		if title != "" || body != "" {
			out = append(out, Section{Title: title, Body: body})
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

// ContextPrompt starts a conversation when there is no overview to go on
// from.
func ContextPrompt(scope, diffstat string) string {
	return fmt.Sprintf("The change set under review is %s\n\nDiffstat:\n```\n%s\n```\n\nRead the changes you need before you answer.\n\n", scope, strings.TrimSpace(diffstat))
}

// QuestionPrompt is the message of a thread sent to the agent. A session
// that does not know the thread yet, a new or a forked one, gets what the
// thread is about and what was said in it so far.
func QuestionPrompt(t *Thread, text string, known bool) string {
	if known {
		return text
	}
	var b strings.Builder
	b.WriteString("This is a new, separate discussion thread. ")
	switch t.Anchor.Kind {
	case AnchorLines:
		fmt.Fprintf(&b, "It is about %s.\n", t.Anchor.Label())
		if t.Anchor.Quote != "" {
			fmt.Fprintf(&b, "\n```\n%s\n```\n", t.Anchor.Quote)
		}
	case AnchorFile:
		fmt.Fprintf(&b, "It is about the changes in the file %s.\n", t.Anchor.File)
	case AnchorSection:
		fmt.Fprintf(&b, "It is about this part of your overview: %q.\n", t.Anchor.Section)
		if t.Anchor.Quote != "" {
			fmt.Fprintf(&b, "\n%s\n", quote(t.Anchor.Quote))
		}
	default:
		b.WriteString("It is about the change set as a whole.\n")
	}
	if earlier := history(t); earlier != "" {
		b.WriteString("\nThe thread so far:\n\n")
		b.WriteString(earlier)
	}
	b.WriteString("\nThe reviewer asks:\n\n")
	b.WriteString(text)
	return b.String()
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
