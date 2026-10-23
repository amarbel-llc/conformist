package buildinfo

import "testing"

func TestLineJoinsNameVersionAndCommit(t *testing.T) {
	if got, want := Line("tool", "1.2.3", "abc1234"), "tool 1.2.3+abc1234"; got != want {
		t.Fatalf("Line = %q, want %q", got, want)
	}
}
