package app

import (
	"testing"

	"github.com/psd-coder/agentdiff/internal/gitx"
)

func TestComparisonCommitBase(t *testing.T) {
	cases := []struct {
		name string
		c    comparison
		want string
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
		if got := tc.c.commitBase(); got != tc.want {
			t.Errorf("%s: commitBase = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestComparisonPick(t *testing.T) {
	rng := gitx.Scope{Kind: gitx.ScopeRange, From: "a", To: "b"}
	c := comparison{branch: "main", base: "main", rng: &rng}

	s, all := c.pick("feature")
	want := gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Head: "feature"}
	if !all || s != want || c.rng != nil {
		t.Fatalf("pick other branch: scope %+v, all %v, range %v", s, all, c.rng)
	}

	if _, all := c.pick("main"); all || c.head != "" || !c.uncommittedShown() {
		t.Errorf("pick checked-out branch: all %v, head %q", all, c.head)
	}
}

func TestComparisonCheckedOut(t *testing.T) {
	t.Run("keeps comparing the branch that was checked out", func(t *testing.T) {
		c := comparison{branch: "feature", base: "main"}
		cur := gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Branch: "feature"}
		to, ok := c.checkedOut("main", []string{"main", "feature"}, cur)
		want := gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Head: "feature"}
		if !ok || to != want || c.head != "feature" || c.branch != "main" {
			t.Errorf("scope %+v, ok %v, head %q, branch %q", to, ok, c.head, c.branch)
		}
	})
	t.Run("reads the picked branch from HEAD once it is checked out", func(t *testing.T) {
		c := comparison{branch: "main", head: "feature", base: "main"}
		cur := gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Head: "feature"}
		to, ok := c.checkedOut("feature", nil, cur)
		want := gitx.Scope{Kind: gitx.ScopeBranch, Base: "main", Branch: "feature"}
		if !ok || to != want || c.head != "" {
			t.Errorf("scope %+v, ok %v, head %q", to, ok, c.head)
		}
	})
	t.Run("drops the picked branch once it is checked out outside a branch scope", func(t *testing.T) {
		c := comparison{branch: "main", head: "feature"}
		if _, ok := c.checkedOut("feature", nil, gitx.Scope{Kind: gitx.ScopeUncommitted}); ok || c.head != "" {
			t.Errorf("ok %v, head %q", ok, c.head)
		}
	})
	t.Run("keeps another picked branch outside a branch scope", func(t *testing.T) {
		c := comparison{branch: "main", head: "feature"}
		if _, ok := c.checkedOut("docs", nil, gitx.Scope{Kind: gitx.ScopeCommit, Commit: "abc"}); ok || c.head != "feature" {
			t.Errorf("ok %v, head %q", ok, c.head)
		}
	})
}
