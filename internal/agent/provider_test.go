package agent

import "testing"

func TestProviderOfOldReviewReadsAsClaude(t *testing.T) {
	cases := []struct {
		provider    Provider
		name, least string
	}{
		{Claude, "Claude Code", "low"},
		{Codex, "Codex", "minimal"},
		{"", "Claude Code", "low"},
	}
	for _, tc := range cases {
		if got := tc.provider.Name(); got != tc.name {
			t.Errorf("Name of %q = %q, want %q", tc.provider, got, tc.name)
		}
		if got := tc.provider.Efforts(); len(got) == 0 || got[0] != tc.least {
			t.Errorf("Efforts of %q = %q, want first %q", tc.provider, got, tc.least)
		}
	}
}
