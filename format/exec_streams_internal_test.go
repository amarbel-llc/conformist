package format //nolint:testpackage // exercises the unexported invocation

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/expand"
)

// writeScript writes an executable script into dir and returns its path.
func writeScript(t *testing.T, dir, body string) string { //testui:allow // internal helper
	t.Helper()

	// Absolute bash, never /usr/bin/env: the godyn test sandbox has neither
	// (conformist-testing(7)).
	bash, err := exec.LookPath("bash")
	require.NoError(t, err)

	path := filepath.Join(dir, "tool.sh")
	require.NoError(t, os.WriteFile(path, []byte("#!"+bash+"\n"+body), 0o755)) //nolint:gosec // test script

	return path
}

// TestRunKeepsStreamsInterleaved pins what conformist#127 must not change:
// invocation.run, used by every formatter and every linter's check path, still
// reports stdout and stderr fused in the order the tool wrote them.
func TestRunKeepsStreamsInterleaved(t *testing.T) {
	dir := t.TempDir()
	tool := writeScript(t, dir, "echo one\necho two >&2\necho three\nexit 1\n")

	inv, err := newInvocation("tool", dir, expand.ListEnviron(os.Environ()...), tool)
	require.NoError(t, err)

	nonzero, output, err := inv.run(context.Background(), dir, nil)
	require.NoError(t, err)
	require.True(t, nonzero)
	require.Equal(t, "one\ntwo\nthree\n", output)
}

// TestRunSplitSeparatesStderr is the new capability: the same invocation, with
// the streams kept apart so a caller can surface the complaint and keep the
// narration quiet. Both the bare-executable and the shell-line path are covered.
func TestRunSplitSeparatesStderr(t *testing.T) {
	dir := t.TempDir()
	tool := writeScript(t, dir, "echo narration\necho complaint >&2\n")

	for name, command := range map[string]string{
		"bare executable": tool,
		"shell line":      tool + " \"$@\"",
	} {
		t.Run(name, func(t *testing.T) {
			inv, err := newInvocation("tool", dir, expand.ListEnviron(os.Environ()...), command)
			require.NoError(t, err)
			require.Equal(t, name == "shell line", inv.isShell(), "the case must exercise the path it names")

			nonzero, stdout, stderr, err := inv.runSplit(context.Background(), dir, nil)
			require.NoError(t, err)
			require.False(t, nonzero)
			require.Equal(t, "narration\n", stdout)
			require.Equal(t, "complaint\n", stderr)
		})
	}
}
