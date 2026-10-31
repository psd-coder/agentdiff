package review

import (
	"fmt"
	"strings"
)

// Label names an anchor as revdiff does: path:43 (+), path:43-67 (-),
// path (file-level).
func (a Anchor) Label() string {
	switch a.Kind {
	case AnchorLines:
		lines := fmt.Sprint(a.Start)
		if a.End > a.Start {
			lines = fmt.Sprintf("%d-%d", a.Start, a.End)
		}
		mark := "+"
		if a.Side == SideOld {
			mark = "-"
		}
		return fmt.Sprintf("%s:%s (%s)", a.File, lines, mark)
	case AnchorFile:
		return a.File + " (file-level)"
	case AnchorSection:
		return "Overview: " + a.Section
	default:
		return "General"
	}
}

type ExportOptions struct {
	Resolved bool
	Agent    bool
}

// Export writes the threads of a review as Markdown for the coding agent
// to act on: one heading per thread, the reviewer's words, and what the
// review agent answered when Agent is set.
func Export(r *Review, scope string, opt ExportOptions) string {
	threads := ExportedThreads(r, opt)
	var b strings.Builder
	b.WriteString("# Code review feedback\n\n")
	fmt.Fprintf(&b, "Scope: %s\n\n", scope)
	if len(threads) == 0 {
		b.WriteString("No comments.\n")
		return b.String()
	}
	b.WriteString("Lines are 1-based. (+) marks lines of the new version, (-) lines of the old version. Address each item, or explain why not.\n")
	for _, t := range threads {
		fmt.Fprintf(&b, "\n## %s\n", t.Anchor.Label())
		if t.Anchor.Kind == AnchorLines && t.Anchor.Quote != "" {
			fmt.Fprintf(&b, "```\n%s\n```\n", strings.TrimRight(t.Anchor.Quote, "\n"))
		}
		writeMessages(&b, t, opt.Agent)
	}
	return b.String()
}

func ExportedThreads(r *Review, opt ExportOptions) []*Thread {
	var out []*Thread
	for _, t := range r.Threads {
		if t.Excluded || (t.Resolved && !opt.Resolved) || !hasUserText(t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func hasUserText(t *Thread) bool {
	for _, m := range t.Messages {
		if m.Role == RoleUser && strings.TrimSpace(m.Text) != "" {
			return true
		}
	}
	return false
}

func writeMessages(b *strings.Builder, t *Thread, withAgent bool) {
	if !withAgent || !t.Discussed() {
		for _, m := range t.Messages {
			if m.Role == RoleUser {
				fmt.Fprintf(b, "%s\n", strings.TrimSpace(m.Text))
			}
		}
		return
	}
	for _, m := range t.Messages {
		if m.Role == RoleAgent && m.Text == "" {
			continue
		}
		who := "Reviewer"
		if m.Role == RoleAgent {
			who = "Review agent"
		}
		fmt.Fprintf(b, "\n**%s:**\n%s\n", who, strings.TrimSpace(m.Text))
	}
}
