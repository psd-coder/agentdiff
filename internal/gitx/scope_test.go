package gitx

import (
	"regexp"
	"strings"
	"testing"
)

func TestScopeKey(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name  string
		scope Scope
		want  string
	}{
		{"uncommitted", Scope{Kind: ScopeUncommitted}, "uncommitted"},
		{"plain branch", Scope{Kind: ScopeBranch, Base: "main"}, "branch-main"},
		{"checked-out branch", Scope{Kind: ScopeBranch, Base: "main", Branch: "dev"}, "branch-dev-vs-main"},
		{"other branch", Scope{Kind: ScopeBranch, Base: "main", Head: "dev"}, "branch-dev-vs-main"},
		{"commit uses 12 chars", Scope{Kind: ScopeCommit, Commit: sha}, "commit-0123456789ab"},
		{"plain range", Scope{Kind: ScopeRange, From: "v1.0", To: "v2.0"}, "range-v1.0-v2.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.scope.Key(); got != tt.want {
				t.Errorf("Key() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScopeKeySanitizes(t *testing.T) {
	safe := regexp.MustCompile(`^[a-z0-9._-]+$`)
	scopes := []Scope{
		{Kind: ScopeBranch, Base: "origin/main"},
		{Kind: ScopeBranch, Base: "../../etc/passwd"},
		{Kind: ScopeBranch, Base: "Feature/Ünïcode branch"},
		{Kind: ScopeBranch, Base: "Main"},
		{Kind: ScopeBranch, Base: "main"},
		{Kind: ScopeBranch, Base: strings.Repeat("long", 40)},
		{Kind: ScopeRange, From: "feature-x", To: "y"},
		{Kind: ScopeRange, From: "feature", To: "x-y"},
		{Kind: ScopeRange, From: "HEAD~3", To: "HEAD"},
		{Kind: ScopeBranch, Base: ""},
	}
	seen := map[string]Scope{}
	for _, s := range scopes {
		key := s.Key()
		if !safe.MatchString(key) {
			t.Errorf("Key() of %+v = %q, not filesystem-safe", s, key)
		}
		if len(key) > 120 {
			t.Errorf("Key() of %+v is %d chars long", s, len(key))
		}
		if prev, ok := seen[key]; ok {
			t.Errorf("Key() collision %q for %+v and %+v", key, prev, s)
		}
		seen[key] = s
		if s.Key() != key {
			t.Errorf("Key() of %+v is not stable", s)
		}
	}
}

func TestScopeLabel(t *testing.T) {
	sha := "abc1234def5678abc1234def5678abc1234def56"
	tests := []struct {
		scope Scope
		want  string
	}{
		{Scope{Kind: ScopeUncommitted}, "Uncommitted changes"},
		{Scope{Kind: ScopeBranch, Base: "main"}, "Branch vs main"},
		{Scope{Kind: ScopeCommit, Commit: sha}, "Commit abc1234"},
		{Scope{Kind: ScopeRange, From: sha, To: "def"}, "abc1234..def"},
	}
	for _, tt := range tests {
		if got := tt.scope.Label(); got != tt.want {
			t.Errorf("Label() of %+v = %q, want %q", tt.scope, got, tt.want)
		}
	}
}

func TestScopeDescribe(t *testing.T) {
	tests := []struct {
		scope Scope
		want  string
	}{
		{Scope{Kind: ScopeUncommitted}, "`git diff HEAD`"},
		{Scope{Kind: ScopeBranch, Base: "main"}, "`git diff main...HEAD`"},
		{Scope{Kind: ScopeCommit, Commit: "abc"}, "`git show abc`"},
		{Scope{Kind: ScopeRange, From: "a", To: "b"}, "`git diff a b`"},
	}
	for _, tt := range tests {
		if got := tt.scope.Describe(); !strings.Contains(got, tt.want) {
			t.Errorf("Describe() of %+v = %q, want it to contain %q", tt.scope, got, tt.want)
		}
	}
}
