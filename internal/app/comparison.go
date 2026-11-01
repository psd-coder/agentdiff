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
func (c *comparison) headBranch() string { return cmp.Or(c.head, c.branch) }

// commitBase is the base the commit list is relative to: on a feature
// branch the commits not on the base yet, else the latest commits.
func (c *comparison) commitBase() string {
	head := c.headBranch()
	if c.base == "" || head == "" || c.base == head || !c.baseSet && strings.HasSuffix(c.base, "/"+head) {
		return ""
	}
	return c.base
}

// listRange is the base and the head of the commit list.
func (c *comparison) listRange() (string, string) {
	if c.rng != nil {
		return c.rng.From, c.rng.To
	}
	return c.commitBase(), c.head
}

// allScope is every change of the commit list at once: the custom range,
// or the branch since its base.
func (c *comparison) allScope() (gitx.Scope, bool) {
	if c.rng != nil {
		return *c.rng, true
	}
	return c.branchScope()
}

// uncommittedShown reports whether the sidebar lists the uncommitted
// changes, which belong to the checked-out branch.
func (c *comparison) uncommittedShown() bool { return c.rng == nil && c.head == "" }

// branchScope is the changes of the branch under review since its base.
func (c *comparison) branchScope() (gitx.Scope, bool) {
	base := c.commitBase()
	if base == "" {
		return gitx.Scope{}, false
	}
	return c.branchScopeOf(c.headBranch(), base), true
}

// branchScopeOf is the changes of a branch since base. The checked-out
// branch is read from HEAD, any other one by its name.
func (c *comparison) branchScopeOf(branch, base string) gitx.Scope {
	if branch == c.branch {
		return gitx.Scope{Kind: gitx.ScopeBranch, Base: base, Branch: branch}
	}
	return gitx.Scope{Kind: gitx.ScopeBranch, Base: base, Head: branch}
}

// label names what the commit list shows: the branch and its base, or the
// custom range.
func (c *comparison) label() (string, string) {
	if c.rng != nil {
		return c.rng.Label(), ""
	}
	if base := c.commitBase(); base != "" {
		return c.headBranch(), "vs " + base
	}
	return c.headBranch(), ""
}

// pick compares branch with the base and drops the custom range. It
// returns all the changes of the branch, when there is a base to compare
// with.
func (c *comparison) pick(branch string) (gitx.Scope, bool) {
	c.head, c.rng = branch, nil
	if branch == c.branch {
		c.head = ""
	}
	return c.allScope()
}

// compare compares branch with base, as picked in the Compare dialog.
func (c *comparison) compare(branch, base string) (gitx.Scope, bool) {
	c.base, c.baseSet = base, true
	return c.pick(branch)
}

// setRange compares two revisions.
func (c *comparison) setRange(from, to string) gitx.Scope {
	s := gitx.Scope{Kind: gitx.ScopeRange, From: from, To: to}
	c.rng = &s
	return s
}

// checkedOut follows a newly checked-out branch. A branch scope cur keeps
// comparing the same branch: it returns the scope that names that branch
// now, and true.
func (c *comparison) checkedOut(branch string, branches []string, cur gitx.Scope) (gitx.Scope, bool) {
	c.branch, c.branches = branch, branches
	if cur.Kind != gitx.ScopeBranch || cur.Head == "" && cur.Branch == "" {
		if c.head == branch {
			c.head = ""
		}
		return gitx.Scope{}, false
	}
	name := cmp.Or(cur.Head, cur.Branch)
	c.head = name
	if name == branch {
		c.head = ""
	}
	return c.branchScopeOf(name, cur.Base), true
}
