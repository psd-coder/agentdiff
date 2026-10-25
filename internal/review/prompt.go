package review

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

const DefaultQuestions = `Explain the changes:

1. Explain on high level.
2. Explain data changes.
3. Explain UI changes and how to see them.
4. Explain architecture and API changes.
5. Explain non-beautiful changes.
6. Tell about assumptions and rules which force non-beautiful changes.`

const SystemPrompt = `You help a developer review code changes that a coding agent made. You only read: never edit files, never run commands that change the repository. Inspect changes with read-only git commands (git diff, git show, git log, git blame) and by reading files. Be concise and concrete. Reference code as path:line.`

// OverviewPrompt asks for the overview and for the files grouped by
// intent, in a JSON block that ParseOverview takes out again.
func OverviewPrompt(scope, diffstat string, files []string, questions string) string {
	if strings.TrimSpace(questions) == "" {
		questions = DefaultQuestions
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The change set to review is %s\n\n", scope)
	fmt.Fprintf(&b, "Diffstat:\n```\n%s\n```\n\n", strings.TrimSpace(diffstat))
	b.WriteString("Read the actual changes before you answer.\n\n")
	b.WriteString(strings.TrimSpace(questions))
	b.WriteString("\n\nFormat: Markdown. Give each point its own level-2 heading (`## ...`). Prefer short bullets to paragraphs. Write \"None.\" under a point that does not apply.\n\n")
	b.WriteString("Finally, group the changed files by intent, in the order a reviewer should read them (for example: core logic first, then its callers, then tests and config). End your answer with exactly one fenced block:\n\n")
	b.WriteString("```json\n{\"groups\":[{\"title\":\"short title\",\"summary\":\"one sentence about what this group does\",\"files\":[\"path/one\",\"path/two\"]}]}\n```\n\n")
	b.WriteString("Put every changed file in exactly one group. The changed files are:\n")
	for _, f := range files {
		b.WriteString("- " + f + "\n")
	}
	return b.String()
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
	cur := Section{}
	inFence := false
	flush := func() {
		cur.Body = strings.TrimSpace(cur.Body)
		if cur.Title != "" || cur.Body != "" {
			out = append(out, cur)
		}
	}
	for line := range strings.SplitSeq(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		}
		if !inFence && strings.HasPrefix(line, "## ") {
			flush()
			cur = Section{Title: strings.TrimSpace(strings.TrimPrefix(line, "## "))}
			continue
		}
		cur.Body += line + "\n"
	}
	flush()
	return out
}

// ContextPrompt starts a conversation when there is no overview to go on
// from.
func ContextPrompt(scope, diffstat string) string {
	return fmt.Sprintf("The change set under review is %s\n\nDiffstat:\n```\n%s\n```\n\nRead the changes you need before you answer.\n\n", scope, strings.TrimSpace(diffstat))
}

// QuestionPrompt is the message of a thread sent to the agent: the first
// one says what the thread is about.
func QuestionPrompt(t *Thread, text string) string {
	if len(t.Messages) > 1 {
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
	b.WriteString("\nThe reviewer asks:\n\n")
	b.WriteString(text)
	return b.String()
}

func quote(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}
