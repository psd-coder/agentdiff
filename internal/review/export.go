package review

import (
	"fmt"
	"strings"
)

// Label names an anchor as revdiff does: path:43 (+), path:43-67 (-),
// path (file-level).
func (anchor Anchor) Label() string {
	switch anchor.Kind {
	case AnchorLines:
		lines := fmt.Sprint(anchor.Start)
		if anchor.End > anchor.Start {
			lines = fmt.Sprintf("%d-%d", anchor.Start, anchor.End)
		}
		mark := "+"
		if anchor.Side == SideOld {
			mark = "-"
		}
		return fmt.Sprintf("%s:%s (%s)", anchor.File, lines, mark)
	case AnchorFile:
		return anchor.File + " (file-level)"
	case AnchorSection:
		return "Overview: " + anchor.Section
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
func Export(review *Review, scope string, opt ExportOptions) string {
	threads := ExportedThreads(review, opt)
	var builder strings.Builder
	builder.WriteString("# Code review feedback\n\n")
	fmt.Fprintf(&builder, "Scope: %s\n\n", scope)
	if len(threads) == 0 {
		builder.WriteString("No comments.\n")
		return builder.String()
	}
	builder.WriteString("Lines are 1-based. (+) marks lines of the new version, (-) lines of the old version. Address each item, or explain why not.\n")
	for _, t := range threads {
		fmt.Fprintf(&builder, "\n## %s\n", t.Anchor.Label())
		if t.Anchor.Kind == AnchorLines && t.Anchor.Quote != "" {
			fmt.Fprintf(&builder, "```\n%s\n```\n", strings.TrimRight(t.Anchor.Quote, "\n"))
		}
		writeMessages(&builder, t, opt.Agent)
	}
	return builder.String()
}

func ExportedThreads(review *Review, opt ExportOptions) []*Thread {
	var out []*Thread
	for _, t := range review.Threads {
		if t.Excluded || (t.Resolved && !opt.Resolved) || !hasUserText(t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

func hasUserText(thread *Thread) bool {
	for _, m := range thread.Messages {
		if m.Role == RoleUser && strings.TrimSpace(m.Text) != "" {
			return true
		}
	}
	return false
}

func writeMessages(builder *strings.Builder, thread *Thread, withAgent bool) {
	if !withAgent || !thread.Discussed() {
		for _, m := range thread.Messages {
			if m.Role == RoleUser {
				fmt.Fprintf(builder, "%s\n", strings.TrimSpace(m.Text))
			}
		}
		return
	}
	for _, m := range thread.Messages {
		if m.Role == RoleAgent && m.Text == "" {
			continue
		}
		who := "Reviewer"
		if m.Role == RoleAgent {
			who = "Review agent"
		}
		fmt.Fprintf(builder, "\n**%s:**\n%s\n", who, strings.TrimSpace(m.Text))
	}
}
