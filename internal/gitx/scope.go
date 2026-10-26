package gitx

import (
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
)

type Scope struct {
	Kind ScopeKind `json:"kind"`
	Base string    `json:"base,omitempty"`
	// Head is the branch a branch scope reviews; empty means HEAD.
	Head   string `json:"head,omitempty"`
	Commit string `json:"commit,omitempty"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
}

// Key returns a stable id that is safe as a file name, also on case-insensitive file systems.
func (s Scope) Key() string {
	switch s.Kind {
	case ScopeUncommitted:
		return "uncommitted"
	case ScopeBranch:
		if s.Head != "" {
			return "branch-" + keyPart(s.Head) + "-vs-" + keyPart(s.Base)
		}
		return "branch-" + keyPart(s.Base)
	case ScopeCommit:
		return "commit-" + keyPart(truncate(s.Commit, 12))
	case ScopeRange:
		return "range-" + keyPart(s.From) + "-" + keyPart(s.To)
	}
	return "unknown-" + keyPart(string(s.Kind))
}

const maxKeyPart = 48

// keyPart keeps [a-z0-9._] and appends a hash of the raw ref whenever it had to change
// anything. That keeps keys unique ("-" is the part separator) and readable.
func keyPart(ref string) string {
	var b strings.Builder
	for _, c := range ref {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.':
			b.WriteRune(c)
		case c >= 'A' && c <= 'Z':
			b.WriteRune(c + ('a' - 'A'))
		default:
			b.WriteByte('_')
		}
	}
	part := truncate(b.String(), maxKeyPart)
	if part == ref && part != "" {
		return part
	}
	h := fnv.New32a()
	h.Write([]byte(ref))
	return fmt.Sprintf("%s_%08x", part, h.Sum32())
}

func (s Scope) Label() string {
	switch s.Kind {
	case ScopeUncommitted:
		return "Uncommitted changes"
	case ScopeBranch:
		if s.Head != "" {
			return s.Head + " vs " + s.Base
		}
		return "Branch vs " + s.Base
	case ScopeCommit:
		return "Commit " + shortRef(s.Commit)
	case ScopeRange:
		return shortRef(s.From) + ".." + shortRef(s.To)
	}
	return string(s.Kind)
}

func (s Scope) Describe() string {
	switch s.Kind {
	case ScopeUncommitted:
		return "the uncommitted changes in the working tree: `git diff HEAD` plus untracked files listed by `git ls-files --others --exclude-standard`"
	case ScopeBranch:
		if s.Head != "" {
			return fmt.Sprintf("the changes on branch `%s` since it diverged from `%s`: `git diff %s...%s` (commits listed by `git log %s..%s`). This branch is not checked out, so the files in the working directory belong to another branch: read the reviewed version of a file with `git show %s:<path>`", s.Head, s.Base, s.Base, s.Head, s.Base, s.Head, s.Head)
		}
		return fmt.Sprintf("the changes on the current branch since it diverged from `%s`: `git diff %s...HEAD` (commits listed by `git log %s..HEAD`)", s.Base, s.Base, s.Base)
	case ScopeCommit:
		return fmt.Sprintf("the changes introduced by commit %s: `git show %s`", s.Commit, s.Commit)
	case ScopeRange:
		return fmt.Sprintf("the changes between `%s` and `%s`: `git diff %s %s` (commits listed by `git log %s..%s`)", s.From, s.To, s.From, s.To, s.From, s.To)
	}
	return "an unknown change set"
}

func shortRef(ref string) string {
	if len(ref) == 40 && isHex(ref) {
		return ref[:7]
	}
	return ref
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
