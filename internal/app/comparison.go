package app

import (
	"cmp"
	"strings"

	"github.com/psd-coder/agentdiff/internal/gitx"
)

// comparison is what the commit list compares: a branch against its base,
// or a custom range. The app reads the branches from git and acts on the
// scopes that its methods return.
type comparison struct {
	// branch is the checked-out branch.
	branch string
	// head is the branch picked to compare with base; empty means the
	// checked-out one.
	head string
	base string
	// baseSet is true once the user picked the base, which then counts
	// even as the upstream of the branch.
	baseSet bool
	// rng is the custom range under review instead of a branch.
	rng       *gitx.Scope
	branches  []string
	worktrees []gitx.Worktree
	// err is why git could not list the branches or worktrees.
	err string
}

// headBranch is the branch under review: the picked one, or the one
// checked out.
func (comparison *comparison) headBranch() string { return cmp.Or(comparison.head, comparison.branch) }

// commitBase is the base the commit list is relative to: on a feature
// branch the commits not on the base yet, else the latest commits.
func (comparison *comparison) commitBase() string {
	head := comparison.headBranch()
	if comparison.base == "" || head == "" || comparison.base == head || !comparison.baseSet && strings.HasSuffix(comparison.base, "/"+head) {
		return ""
	}
	return comparison.base
}

// listRange is the base and the head of the commit list.
func (comparison *comparison) listRange() (string, string) {
	if comparison.rng != nil {
		return comparison.rng.From, comparison.rng.To
	}
	return comparison.commitBase(), comparison.head
}

// allScope is every change of the commit list at once: the custom range,
// or the branch since its base.
func (comparison *comparison) allScope() (gitx.Scope, bool) {
	if comparison.rng != nil {
		return *comparison.rng, true
	}
	return comparison.branchScope()
}

// uncommittedShown reports whether the sidebar lists the uncommitted
// changes, which belong to the checked-out branch.
func (comparison *comparison) uncommittedShown() bool {
	return comparison.rng == nil && comparison.head == ""
}

// branchScope is the changes of the branch under review since its base.
func (comparison *comparison) branchScope() (gitx.Scope, bool) {
	base := comparison.commitBase()
	if base == "" {
		return gitx.Scope{}, false
	}
	return comparison.branchScopeOf(comparison.headBranch(), base), true
}

// branchScopeOf is the changes of a branch since base. The checked-out
// branch is read from HEAD, any other one by its name.
func (comparison *comparison) branchScopeOf(branch, base string) gitx.Scope {
	if branch == comparison.branch {
		return gitx.Scope{Kind: gitx.ScopeBranch, Base: base, Branch: branch}
	}
	return gitx.Scope{Kind: gitx.ScopeBranch, Base: base, Head: branch}
}

// label names what the commit list shows: the branch and its base, or the
// custom range.
func (comparison *comparison) label() (string, string) {
	if comparison.rng != nil {
		return comparison.rng.Label(), ""
	}
	if base := comparison.commitBase(); base != "" {
		return comparison.headBranch(), "vs " + base
	}
	return comparison.headBranch(), ""
}

// pick compares branch with the base and drops the custom range. It
// returns all the changes of the branch, when there is a base to compare
// with.
func (comparison *comparison) pick(branch string) (gitx.Scope, bool) {
	comparison.head, comparison.rng = branch, nil
	if branch == comparison.branch {
		comparison.head = ""
	}
	return comparison.allScope()
}

// compare compares branch with base, as picked in the Compare dialog.
func (comparison *comparison) compare(branch, base string) (gitx.Scope, bool) {
	comparison.base, comparison.baseSet = base, true
	return comparison.pick(branch)
}

// setRange compares two revisions.
func (comparison *comparison) setRange(from, to string) gitx.Scope {
	scope := gitx.Scope{Kind: gitx.ScopeRange, From: from, To: to}
	comparison.rng = &scope
	return scope
}

// checkedOut follows a newly checked-out branch. A branch scope cur keeps
// comparing the same branch: it returns the scope that names that branch
// now, and true.
func (comparison *comparison) checkedOut(branch string, branches []string, cur gitx.Scope) (gitx.Scope, bool) {
	comparison.branch, comparison.branches = branch, branches
	if cur.Kind != gitx.ScopeBranch || cur.Head == "" && cur.Branch == "" {
		if comparison.head == branch {
			comparison.head = ""
		}
		return gitx.Scope{}, false
	}
	name := cmp.Or(cur.Head, cur.Branch)
	comparison.head = name
	if name == branch {
		comparison.head = ""
	}
	return comparison.branchScopeOf(name, cur.Base), true
}

// branchRow is a branch in the branch list.
type branchRow struct {
	name string
	// tree is the path of another worktree that has the branch checked
	// out.
	tree       string
	checkedOut bool
	// chosen is set on the branch under review.
	chosen bool
}

// branchRows are the branches whose name holds query, ignoring case. root
// is the worktree of the open repository.
func (comparison *comparison) branchRows(query, root string) []branchRow {
	trees := map[string]string{}
	for _, w := range comparison.worktrees {
		if w.Path != root {
			trees[w.Branch] = w.Path
		}
	}
	lowerQuery := strings.ToLower(query)
	var rows []branchRow
	for _, b := range comparison.branches {
		if !strings.Contains(strings.ToLower(b), lowerQuery) {
			continue
		}
		rows = append(rows, branchRow{
			name:       b,
			tree:       trees[b],
			checkedOut: b == comparison.branch,
			chosen:     comparison.rng == nil && b == comparison.headBranch(),
		})
	}
	return rows
}

// tag is the note next to the name of the branch.
func (branchRow branchRow) tag() string {
	switch {
	case branchRow.checkedOut:
		return "checked out"
	case branchRow.tree != "":
		return "worktree"
	}
	return ""
}

// label is what a screen reader says for the row.
func (branchRow branchRow) label() string {
	label := branchRow.name
	if tag := branchRow.tag(); tag != "" {
		label += ", " + tag
	}
	if branchRow.chosen {
		label += ", selected"
	}
	return label
}
