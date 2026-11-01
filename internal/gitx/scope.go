package gitx

import (
	"cmp"
	"fmt"
	"hash/fnv"
	"strings"
)

type ScopeKind string

const (
	ScopeUncommitted ScopeKind = "uncommitted"
	ScopeBranch      ScopeKind = "branch"
	ScopeCommit      ScopeKind = "commit"
	ScopeRange       ScopeKind = "range"
	// ScopeArchived holds a review of uncommitted changes that are gone
	// from the working tree, under the ID in From. It has no changes of
	// its own.
	ScopeArchived ScopeKind = "archived"
)

type Scope struct {
	Kind ScopeKind `json:"kind"`
	Base string    `json:"base,omitempty"`
	// Head is the branch a branch scope reviews when it is not checked
	// out; empty means HEAD.
	Head string `json:"head,omitempty"`
	// Branch names the checked-out branch a branch scope with an empty
	// Head reviews, so its review stays with the branch.
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit,omitempty"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
}

// ArchivedScope is the scope of the archived review with id.
func ArchivedScope(id string) Scope { return Scope{Kind: ScopeArchived, From: id} }

// Key returns a stable id that is safe as a file name, also on case-insensitive file systems.
func (scope Scope) Key() string {
	switch scope.Kind {
	case ScopeUncommitted:
		return "uncommitted"
	case ScopeBranch:
		if branch := scope.branchName(); branch != "" {
			return "branch-" + keyPart(branch) + "-vs-" + keyPart(scope.Base)
		}
		return "branch-" + keyPart(scope.Base)
	case ScopeCommit:
		return "commit-" + keyPart(truncate(scope.Commit, 12))
	case ScopeRange:
		return "range-" + keyPart(scope.From) + "-" + keyPart(scope.To)
	case ScopeArchived:
		return "archived-" + keyPart(scope.From)
	}
	return "unknown-" + keyPart(string(scope.Kind))
}

const maxKeyPart = 48

// keyPart keeps [a-z0-9._] and appends a hash of the raw ref whenever it had to change
// anything. That keeps keys unique ("-" is the part separator) and readable.
func keyPart(ref string) string {
	var sanitized strings.Builder
	for _, c := range ref {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.':
			sanitized.WriteRune(c)
		case c >= 'A' && c <= 'Z':
			sanitized.WriteRune(c + ('a' - 'A'))
		default:
			sanitized.WriteByte('_')
		}
	}
	part := truncate(sanitized.String(), maxKeyPart)
	if part == ref && part != "" {
		return part
	}
	refHash := fnv.New32a()
	refHash.Write([]byte(ref))
	return fmt.Sprintf("%s_%08x", part, refHash.Sum32())
}

func (scope Scope) Label() string {
	switch scope.Kind {
	case ScopeUncommitted:
		return "Uncommitted changes"
	case ScopeBranch:
		if branch := scope.branchName(); branch != "" {
			return branch + " vs " + scope.Base
		}
		return "Branch vs " + scope.Base
	case ScopeCommit:
		return "Commit " + shortRef(scope.Commit)
	case ScopeRange:
		return shortRef(scope.From) + ".." + shortRef(scope.To)
	case ScopeArchived:
		return "Earlier uncommitted changes"
	}
	return string(scope.Kind)
}

func (scope Scope) Describe() string {
	switch scope.Kind {
	case ScopeUncommitted:
		return "the uncommitted changes in the working tree: `git diff HEAD` plus untracked files listed by `git ls-files --others --exclude-standard`"
	case ScopeBranch:
		if scope.Head != "" {
			return fmt.Sprintf("the changes on branch `%s` since it diverged from `%s`: `git diff %s...%s` (commits listed by `git log %s..%s`). This branch is not checked out, so the files in the working directory belong to another branch: read the reviewed version of a file with `git show %s:<path>`", scope.Head, scope.Base, scope.Base, scope.Head, scope.Base, scope.Head, scope.Head)
		}
		return fmt.Sprintf("the changes on the current branch since it diverged from `%s`: `git diff %s...HEAD` (commits listed by `git log %s..HEAD`)", scope.Base, scope.Base, scope.Base)
	case ScopeCommit:
		return fmt.Sprintf("the changes introduced by commit %s: `git show %s`", scope.Commit, scope.Commit)
	case ScopeRange:
		return fmt.Sprintf("the changes between `%s` and `%s`: `git diff %s %s` (commits listed by `git log %s..%s`)", scope.From, scope.To, scope.From, scope.To, scope.From, scope.To)
	default:
		return "an unknown change set"
	}
}

func (scope Scope) branchName() string { return cmp.Or(scope.Head, scope.Branch) }

// NewRev is the revision that holds the new side of the changes, empty
// for the working tree.
func (scope Scope) NewRev() string {
	switch scope.Kind {
	case ScopeBranch:
		return cmp.Or(scope.Head, "HEAD")
	case ScopeCommit:
		return scope.Commit
	case ScopeRange:
		return scope.To
	default:
		return ""
	}
}

func shortRef(ref string) string {
	if len(ref) == 40 && isHex(ref) {
		return ref[:7]
	}
	return ref
}

func isHex(value string) bool {
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func truncate(value string, limit int) string {
	if len(value) > limit {
		return value[:limit]
	}
	return value
}
