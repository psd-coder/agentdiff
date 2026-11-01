package app

import (
	"testing"

	"github.com/psd-coder/agentdiff/internal/gitx"
)

func TestComparisonCommitBase(t *testing.T) {
	cases := []struct {
		name       string
		comparison comparison
		want       string
	}{
		{"without a base", comparison{branch: "feature"}, ""},
		{"without a branch", comparison{base: "main"}, ""},
		{"branch is its own base", comparison{branch: "main", base: "main"}, ""},
		{"picked branch is its own base", comparison{branch: "feature", head: "main", base: "main"}, ""},
		{"upstream of the branch", comparison{branch: "feature", base: "origin/feature"}, ""},
		{"upstream picked as base", comparison{branch: "feature", base: "origin/feature", baseSet: true}, "origin/feature"},
		{"feature branch", comparison{branch: "feature", base: "main"}, "main"},
		{"picked feature branch", comparison{branch: "main", head: "feature", base: "main"}, "main"},
	}
	for _, tc := range cases {
		if got := tc.comparison.commitBase(); got != tc.want {
			t.Errorf("%s: commitBase = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestComparisonPick(t *testing.T) {
	rng := gitx.Scope{Kind: gitx.ScopeRange, From: "a", To: "b"}
	comparison := comparison{branch: "main", base: "main", rng: &rng}

	scope, all := comparison.pick("feature")
	want := gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Head: "feature"}
	if !all || scope != want || comparison.rng != nil {
		t.Fatalf("pick other branch: scope %+v, all %v, range %v", scope, all, comparison.rng)
	}

	if _, all := comparison.pick("main"); all || comparison.head != "" || !comparison.uncommittedShown() {
		t.Errorf("pick checked-out branch: all %v, head %q", all, comparison.head)
	}
}

func TestComparisonCheckedOut(t *testing.T) {
	t.Run("keeps comparing the branch that was checked out", func(t *testing.T) {
		comparison := comparison{branch: "feature", base: "main"}
		cur := gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Branch: "feature"}
		to, ok := comparison.checkedOut("main", []string{"main", "feature"}, cur)
		want := gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Head: "feature"}
		if !ok || to != want || comparison.head != "feature" || comparison.branch != "main" {
			t.Errorf("scope %+v, ok %v, head %q, branch %q", to, ok, comparison.head, comparison.branch)
		}
	})
	t.Run("reads the picked branch from HEAD once it is checked out", func(t *testing.T) {
		comparison := comparison{branch: "main", head: "feature", base: "main"}
		cur := gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Head: "feature"}
		to, ok := comparison.checkedOut("feature", nil, cur)
		want := gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Branch: "feature"}
		if !ok || to != want || comparison.head != "" {
			t.Errorf("scope %+v, ok %v, head %q", to, ok, comparison.head)
		}
	})
	t.Run("drops the picked branch once it is checked out outside a branch scope", func(t *testing.T) {
		comparison := comparison{branch: "main", head: "feature"}
		if _, ok := comparison.checkedOut("feature", nil, gitx.Scope{Kind: gitx.ScopeUncommitted}); ok || comparison.head != "" {
			t.Errorf("ok %v, head %q", ok, comparison.head)
		}
	})
	t.Run("keeps another picked branch outside a branch scope", func(t *testing.T) {
		comparison := comparison{branch: "main", head: "feature"}
		if _, ok := comparison.checkedOut("docs", nil, gitx.Scope{Kind: gitx.ScopeCommit, Commit: "abc"}); ok || comparison.head != "feature" {
			t.Errorf("ok %v, head %q", ok, comparison.head)
		}
	})
}

func TestComparisonBranchRows(t *testing.T) {
	comparison := comparison{
		branch:   "main",
		head:     "Feature-x",
		branches: []string{"main", "Feature-x", "feature-y", "docs"},
		worktrees: []gitx.Worktree{
			{Path: "/repo", Branch: "main"},
			{Path: "/trees/y", Branch: "feature-y"},
			{Path: "/trees/detached"},
		},
	}
	rows := comparison.branchRows("FEATURE", "/repo")
	want := []branchRow{
		{name: "Feature-x", chosen: true},
		{name: "feature-y", tree: "/trees/y"},
	}
	if len(rows) != len(want) || rows[0] != want[0] || rows[1] != want[1] {
		t.Fatalf("rows %+v", rows)
	}

	all := comparison.branchRows("", "/repo")
	if len(all) != 4 || all[0] != (branchRow{name: "main", checkedOut: true}) {
		t.Errorf("rows without a filter %+v", all)
	}

	rng := gitx.Scope{Kind: gitx.ScopeRange, From: "a", To: "b"}
	comparison.rng = &rng
	for _, r := range comparison.branchRows("", "/repo") {
		if r.chosen {
			t.Errorf("%s is chosen while a range is under review", r.name)
		}
	}
}

func TestBranchRowLabel(t *testing.T) {
	cases := []struct {
		row  branchRow
		want string
	}{
		{branchRow{name: "docs"}, "docs"},
		{branchRow{name: "main", checkedOut: true, chosen: true}, "main, checked out, selected"},
		{branchRow{name: "y", tree: "/trees/y"}, "y, worktree"},
	}
	for _, tc := range cases {
		if got := tc.row.label(); got != tc.want {
			t.Errorf("label of %+v = %q, want %q", tc.row, got, tc.want)
		}
	}
}
